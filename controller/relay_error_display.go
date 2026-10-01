package controller

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/settingsaccess"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// relayErrorDisplayScopeApproved reports whether the settings scope that
// RequireSystemSettingsScope authorized for this request is this feature's own
// (or root's unrestricted ""). The middleware checks the permission of
// whichever scope the request names, so without this an admin allowed to view
// any other settings scope could read this feature's status, presets and
// previews. Without the middleware in front (direct calls) only root passes,
// as settingsWriteScope does.
func relayErrorDisplayScopeApproved(c *gin.Context) bool {
	authorized, exists := c.Get(middleware.SystemSettingsScopeContextKey)
	if !exists {
		return c.GetInt("role") == common.RoleRootUser
	}
	scope, ok := authorized.(string)
	return ok && (scope == settingsaccess.ScopeRelayErrorDisplay || (scope == "" && c.GetInt("role") == common.RoleRootUser))
}

type relayErrorPreviewRequest struct {
	Setting operation_setting.RelayErrorDisplaySetting `json:"setting"`
	Sample  struct {
		Source     string `json:"source"`
		StatusCode int    `json:"status_code"`
		ErrorCode  string `json:"error_code"`
		Message    string `json:"message"`
	} `json:"sample"`
}

// PreviewRelayErrorDisplay shows what a user would see for a sample error under
// a draft configuration, evaluated by the same code as live requests. The draft
// is evaluated as if enabled, so rules can be checked before switching them on.
func PreviewRelayErrorDisplay(c *gin.Context) {
	if !relayErrorDisplayScopeApproved(c) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	var request relayErrorPreviewRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	source := strings.TrimSpace(request.Sample.Source)
	if (source != operation_setting.RelayErrorSourceUpstream && source != operation_setting.RelayErrorSourceLocal) ||
		utf8.RuneCountInString(request.Sample.Message) > operation_setting.MaxRelayErrorTextLen {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	decision, err := operation_setting.PreviewRelayErrorDecision(request.Setting, operation_setting.RelayErrorInput{
		Upstream:   source == operation_setting.RelayErrorSourceUpstream,
		StatusCode: request.Sample.StatusCode,
		ErrorCode:  strings.TrimSpace(request.Sample.ErrorCode),
		Message:    request.Sample.Message,
		Lang:       i18n.GetLangFromContext(c),
	})
	if err != nil {
		logger.LogWarn(c, "relay error display preview rejected: "+err.Error())
		respondRelayErrorDisplayInvalid(c, err)
		return
	}
	message, status := request.Sample.Message, request.Sample.StatusCode
	if decision.Replace {
		message = decision.Message
		if decision.StatusCode > 0 {
			status = decision.StatusCode
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"replace":     decision.Replace,
		"message":     message,
		"status_code": status,
		"rule_index":  decision.RuleIndex,
		"rule_name":   decision.RuleName,
	}})
}

// GetRelayErrorDisplayPresets returns the starting rule set the UI can apply,
// in the UI's language (?lang=, falling back to the admin's language setting).
func GetRelayErrorDisplayPresets(c *gin.Context) {
	if !relayErrorDisplayScopeApproved(c) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	lang := i18n.GetLangFromContext(c)
	if uiLang := strings.TrimSpace(c.Query("lang")); uiLang != "" {
		lang = i18n.ParseAcceptLanguage(uiLang)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": operation_setting.RelayErrorPresetRules(lang)})
}

// GetRelayErrorDisplayStatus lists the stored parts the live configuration
// skips because they no longer validate (the rest stays in effect), each with
// a reason in the admin's language that names the rule, so the settings page
// can ask the admin to fix or delete them.
func GetRelayErrorDisplayStatus(c *gin.Context) {
	if !relayErrorDisplayScopeApproved(c) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	skipped := operation_setting.RelayErrorDisplaySkipped()
	items := make([]gin.H, 0, len(skipped))
	for _, part := range skipped {
		var key string
		var args map[string]any
		switch part.Part {
		case operation_setting.RelayErrorSkippedDefaultMessage:
			key = i18n.MsgSettingRelayErrorDefaultMessageSkipped
		case operation_setting.RelayErrorSkippedRules:
			key = i18n.MsgSettingRelayErrorRulesUnreadable
		case operation_setting.RelayErrorSkippedRuleLimit:
			key, args = i18n.MsgSettingRelayErrorRulesOverLimit, map[string]any{"Max": operation_setting.MaxRelayErrorRules}
		default:
			key, args = relayErrorDisplayInvalidMessage(part.Err)
			if key == i18n.MsgSettingRelayErrorDisplayInvalid {
				key, args = i18n.MsgSettingRelayErrorRuleSkipped, map[string]any{"Rule": part.Rule}
			}
		}
		items = append(items, gin.H{"part": part.Part, "rule": part.Rule, "reason": i18n.T(c, key, args)})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"skipped": items}})
}

// respondRelayErrorDisplayInvalid names the rule and step when a find-and-replace
// step is what cannot be saved; other problems get the general message.
func respondRelayErrorDisplayInvalid(c *gin.Context, err error) {
	key, args := relayErrorDisplayInvalidMessage(err)
	common.ApiErrorI18n(c, key, args)
}

// relayErrorDisplayInvalidMessage is the i18n key (and its arguments) that
// explains why a relay error display configuration does not validate.
func relayErrorDisplayInvalidMessage(err error) (string, map[string]any) {
	var stepErr *operation_setting.RelayErrorEditInvalid
	if errors.As(err, &stepErr) {
		key := i18n.MsgSettingRelayErrorEditInvalid
		if stepErr.TooComplex {
			key = i18n.MsgSettingRelayErrorEditTooComplex
		}
		return key, map[string]any{"Rule": stepErr.Rule, "Step": stepErr.Step}
	}
	return i18n.MsgSettingRelayErrorDisplayInvalid, nil
}
