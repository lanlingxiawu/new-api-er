package controller

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// accessTokenMinLifetimeSecs is the shortest expiry a new access token may have.
const accessTokenMinLifetimeSecs = 60 * 60

type accessTokenItem struct {
	model.UserAccessToken
	Scopes []string `json:"scopes"`
}

type accessTokenCreateRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt int64    `json:"expires_at"`
}

// accessTokenUpdateRequest renames a token; a present scopes field also
// replaces its grant.
type accessTokenUpdateRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func newAccessTokenItem(token *model.UserAccessToken) accessTokenItem {
	scopes := token.GetScopes()
	if scopes == nil {
		scopes = []string{}
	}
	return accessTokenItem{UserAccessToken: *token, Scopes: scopes}
}

// writeAccessTokenError responds with a stable code and a web console message.
func writeAccessTokenError(c *gin.Context, status int, code, key string, params ...map[string]any) {
	common.ApiErrorStatus(c, status, common.NewMessage(key, params...), gin.H{"code": code})
}

// normalizeAccessTokenName returns the trimmed name, or false when it is empty
// or longer than the 64-character column.
func normalizeAccessTokenName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	count := utf8.RuneCountInString(name)
	return name, count > 0 && count <= 64
}

func ListAccessTokens(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	tokens, err := model.ListUserAccessTokens(identity.UserID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	items := make([]accessTokenItem, 0, len(tokens))
	for i := range tokens {
		items = append(items, newAccessTokenItem(&tokens[i]))
	}
	var legacy gin.H
	if !model.LegacyAccessTokensRetired(common.GetTimestamp()) {
		status, err := model.GetUserAccessTokenStatus(identity.UserID)
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		if status.Exists {
			legacy = gin.H{
				"token_hint":   status.TokenHint,
				"token_ref":    status.TokenRef,
				"created_at":   status.CreatedAt,
				"last_used_at": status.LastUsedAt,
				"last_used_ip": status.LastUsedIp,
				"retire_at":    model.LegacyAccessTokenRetireAt(),
			}
		}
	}
	common.ApiSuccess(c, gin.H{"items": items, "legacy": legacy})
}

func GetAccessTokenCatalog(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	common.ApiSuccess(c, gin.H{
		"groups":                     service.AccessTokenCatalog(identity.UserID, c.GetInt("role")),
		"max_tokens":                 service.AccessTokenMaxPerUser,
		"default_expiry_days":        service.AccessTokenDefaultExpiryDays,
		"management_max_expiry_days": 30,
		"management_restricted":      c.GetInt("role") >= common.RoleAdminUser,
	})
}

// GetAccessTokenScopes returns the unfiltered scope labels used to describe
// stored grants, such as those in audit records.
func GetAccessTokenScopes(c *gin.Context) {
	if _, ok := requireBrowserSession(c); !ok {
		return
	}
	common.ApiSuccess(c, gin.H{"resources": service.AccessTokenScopeDictionary()})
}

// CreateAccessToken validates the whole request before consuming the proof, so
// a rejected grant never burns the user's verification.
func CreateAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	var req accessTokenCreateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		writeAccessTokenError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body.")
		return
	}
	name, ok := normalizeAccessTokenName(req.Name)
	if !ok {
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_NAME_INVALID", "Access token name must be 1 to 64 characters.")
		return
	}
	now := common.GetTimestamp()
	if req.ExpiresAt != 0 && req.ExpiresAt < now+accessTokenMinLifetimeSecs {
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_EXPIRY_INVALID", "Access token expiry must be at least one hour from now.")
		return
	}
	scopes, err := service.NormalizeAccessTokenScopes(identity.UserID, c.GetInt("role"), req.Scopes)
	switch {
	case errors.Is(err, service.ErrAccessTokenScopeForbidden):
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_FORBIDDEN", "You cannot grant one or more of these permissions.")
		return
	case err != nil:
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_INVALID", "Select at least one valid permission.")
		return
	}
	if slices.ContainsFunc(scopes, service.IsManagementScope) && (req.ExpiresAt <= now || req.ExpiresAt-now > service.AdminTokenMaxLifetimeSeconds) {
		common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("Management tokens must expire within 30 days. Sign in to create a replacement token."), gin.H{"code": "ACCESS_TOKEN_EXPIRY_INVALID", "message": i18n.T(c, i18n.MsgAdminTokenExpiryInvalid)})
		return
	}
	proofContext, err := common.Marshal(service.AccessTokenGenerateContext{Scopes: scopes, ExpiresAt: req.ExpiresAt})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenGenerate, Context: proofContext}) == nil {
		return
	}
	suffix, err := common.GenerateRandomCharsKey(43)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	raw := model.AccessTokenPrefix + suffix
	token := &model.UserAccessToken{
		Name:      name,
		TokenHash: model.AccessTokenFingerprint(raw),
		TokenHint: model.AccessTokenHint(raw),
		ExpiresAt: req.ExpiresAt,
		CreatedAt: now,
	}
	if err := token.SetScopes(scopes); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if err := model.CreateUserAccessToken(identity.UserID, token, service.AccessTokenMaxPerUser); err != nil {
		if errors.Is(err, model.ErrAccessTokenLimit) {
			writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_LIMIT", "You can create at most {{count}} access tokens", map[string]any{"count": service.AccessTokenMaxPerUser})
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	model.RequestAuditChanges(c).Add(model.SelfAuditTarget("access_token", token.Id, identity.UserID, token.Name), nil, model.AccessTokenAuditSnapshot(token))
	recordUserSecurityAudit(c, identity.UserID, "access_token.generate", map[string]any{
		"token_id":   token.Id,
		"name":       token.Name,
		"scopes":     scopes,
		"expires_at": token.ExpiresAt,
		"token_ref":  token.TokenHash,
	})
	common.ApiSuccess(c, gin.H{"token": raw, "item": newAccessTokenItem(token)})
}

// UpdateAccessToken renames a token without verification. Changing the grant
// is checked in full before the proof is consumed, like creation.
func UpdateAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	var req accessTokenUpdateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		writeAccessTokenError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body.")
		return
	}
	name, ok := normalizeAccessTokenName(req.Name)
	if !ok {
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_NAME_INVALID", "Access token name must be 1 to 64 characters.")
		return
	}
	var scopes []string
	previous, err := model.GetUserAccessToken(identity.UserID, id)
	if errors.Is(err, model.ErrAccessTokenNotFound) {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if req.Scopes != nil {
		scopes, err = service.NormalizeAccessTokenScopes(identity.UserID, c.GetInt("role"), req.Scopes)
		normalized, valid := service.NormalizeAccessTokenScopeList(req.Scopes)
		reduction := valid && !slices.ContainsFunc(normalized, func(scope string) bool { return !slices.Contains(previous.GetScopes(), scope) })
		if reduction {
			scopes = normalized
			err = nil
		}
		switch {
		case errors.Is(err, service.ErrAccessTokenScopeForbidden):
			writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_FORBIDDEN", "You cannot grant one or more of these permissions.")
			return
		case err != nil:
			writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_INVALID", "Select at least one valid permission.")
			return
		}
		if !reduction && slices.ContainsFunc(scopes, service.IsManagementScope) && !service.AdminTokenLifetimeValid(previous) {
			common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("Management tokens must expire within 30 days. Sign in to create a replacement token."), gin.H{"code": "ACCESS_TOKEN_EXPIRY_INVALID", "message": i18n.T(c, i18n.MsgAdminTokenExpiryInvalid)})
			return
		}
		proofContext, err := common.Marshal(service.AccessTokenUpdateContext{TokenID: id, Scopes: scopes})
		if err != nil {
			writeSecurityOperationError(c, err)
			return
		}
		if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenUpdate, Context: proofContext}) == nil {
			return
		}
	}
	token, err := model.UpdateUserAccessToken(identity.UserID, id, name, scopes)
	if errors.Is(err, model.ErrAccessTokenNotFound) {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	after := model.AccessTokenAuditSnapshot(previous)
	after["name"] = name
	if req.Scopes != nil {
		after["scopes"] = scopes
	}
	model.RequestAuditChanges(c).Add(model.SelfAuditTarget("access_token", token.Id, identity.UserID, token.Name), model.AccessTokenAuditSnapshot(previous), after)
	if req.Scopes == nil {
		recordUserSecurityAudit(c, identity.UserID, "access_token.rename", map[string]any{"token_id": token.Id, "name": token.Name})
		common.ApiSuccess(c, newAccessTokenItem(token))
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "access_token.update", map[string]any{
		"token_id":        token.Id,
		"name":            token.Name,
		"scopes":          scopes,
		"previous_scopes": previous.GetScopes(),
		"token_ref":       token.TokenHash,
	})
	common.ApiSuccess(c, newAccessTokenItem(token))
}

// DeleteAccessToken checks ownership before consuming the proof, so probing
// other users' token IDs never burns a verification.
func DeleteAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	if _, err := model.GetUserAccessToken(identity.UserID, id); err != nil {
		if errors.Is(err, model.ErrAccessTokenNotFound) {
			writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	proofContext, err := common.Marshal(service.AccessTokenRevokeContext{TokenID: id})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenRevoke, Context: proofContext}) == nil {
		return
	}
	token, err := model.DeleteUserAccessToken(identity.UserID, id)
	if errors.Is(err, model.ErrAccessTokenNotFound) {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	target := model.SelfAuditTarget("access_token", token.Id, identity.UserID, token.Name)
	target.Consistency = "atomic"
	model.RequestAuditChanges(c).Add(target, model.AccessTokenAuditSnapshot(token), nil)
	recordUserSecurityAudit(c, identity.UserID, "access_token.revoke", map[string]any{"token_id": token.Id, "name": token.Name, "token_ref": token.TokenHash})
	common.ApiSuccess(c, nil)
}

// RevokeLegacyAccessToken removes the pre-scope users.access_token credential.
// After the transition deadline the legacy token no longer exists for callers.
func RevokeLegacyAccessToken(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	if model.LegacyAccessTokensRetired(common.GetTimestamp()) {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	status, err := model.GetUserAccessTokenStatus(identity.UserID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if !status.Exists {
		writeAccessTokenError(c, http.StatusNotFound, "ACCESS_TOKEN_NOT_FOUND", model.ErrAccessTokenNotFound.Error())
		return
	}
	proofContext, err := common.Marshal(service.AccessTokenRevokeContext{Legacy: true})
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeAccessTokenRevoke, Context: proofContext}) == nil {
		return
	}
	ref, err := model.RevokeUserAccessToken(identity.UserID)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if ref != "" {
		model.RequestAuditChanges(c).Add(model.SelfAuditTarget("access_token", identity.UserID, identity.UserID, ""), model.AuditFields{"revoked": false}, model.AuditFields{"revoked": true})
		recordUserSecurityAudit(c, identity.UserID, "access_token.revoke", map[string]any{"legacy": true, "token_ref": ref})
	}
	common.ApiSuccess(c, nil)
}

func GetAuditLogs(c *gin.Context) {
	startTimestamp, endTimestamp, validRange := parseLogQueryTimeRange(c)
	if !validRange {
		return
	}
	page := common.GetPageQuery(c)
	if page.Page < 1 || page.PageSize < 1 || page.Page > 100000000 {
		common.ApiErrorMsg(c, "Invalid audit pagination")
		return
	}
	filter, valid := parseAuditFilter(c, false)
	filter.StartTimestamp, filter.EndTimestamp = startTimestamp, endTimestamp
	if !valid || c.Query("ip_risk") != "" {
		common.ApiErrorI18n(c, "audit_filter.invalid")
		return
	}
	viewerRole := c.GetInt("role")
	db, ok := auditQueryDB(c, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	logs, total, err := model.QueryAuditLogs(db.WithContext(ctx), filter, page.GetStartIdx(), page.GetPageSize(), viewerRole)
	if err != nil {
		logger.LogError(c, common.LogText("audit list query failed: %v", err))
		common.ApiErrorI18n(c, "audit_filter.unavailable")
		return
	}
	if filter.SelfView {
		page.SetItems(model.ProjectAuditActivities(logs))
	} else {
		page.SetItems(logs)
	}
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
