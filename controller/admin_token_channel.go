package controller

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// AdminTokenChannelRequest runs before every channel handler. Read responses
// use an explicit SQL projection; writes cannot reach the handler until all
// targets and the exact high-risk action have been authorized.
func AdminTokenChannelRequest(c *gin.Context) {
	if !c.GetBool("use_access_token") {
		c.Next()
		return
	}
	route := c.Request.Method + " " + c.FullPath()
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok {
		writeAdminTokenChannelError(c, service.ErrVerificationForbidden)
		return
	}
	op := service.AdminChannelOperation{Action: route, Query: map[string]string{}}
	if id := c.Param("id"); id != "" {
		var err error
		op.ChannelID, err = strconv.Atoi(id)
		if err != nil || op.ChannelID <= 0 {
			writeAdminTokenChannelError(c, model.ErrChannelNotAccessible)
			return
		}
	}
	for key, values := range c.Request.URL.Query() {
		if len(values) != 1 {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return
		}
		op.Query[key] = values[0]
	}
	if c.Request.Body != nil && c.Request.Method != "GET" {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 2*1024*1024+1))
		if err != nil || len(raw) > 2*1024*1024 {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return
		}
		if len(raw) > 0 {
			op.Body = raw
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	}
	action, err := service.AuthorizeAdminChannelOperation(c.Request.Context(), identity, op)
	if err != nil {
		writeAdminTokenChannelError(c, err)
		return
	}
	c.Set("admin_token_channel_ids", action.IDs)
	c.Set("admin_token_creator_id", identity.UserID)
	if strings.HasPrefix(route, "GET ") && action.Permission.Action == "read" {
		readAdminTokenChannels(c, op, action.IDs)
		c.Abort()
		return
	}
	if action.Scope != "" {
		payload, err := common.Marshal(op)
		if err != nil {
			writeAdminTokenChannelError(c, err)
			return
		}
		authorization := middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: action.Scope, Context: payload})
		if authorization == nil {
			return
		}
		c.Set("admin_token_verification_method", authorization.Method)
	}
	if applyAdminTokenChannelBatch(c, action) {
		c.Abort()
		return
	}
	// Do not expose upstream error bodies, nested channel settings or key previews
	// from legacy management handlers to PATs. Bound buffering to one MiB.
	original := c.Writer
	buffered := &adminTokenResponseWriter{ResponseWriter: original, status: http.StatusOK}
	c.Writer = buffered
	c.Next()
	c.Writer = original
	var response struct {
		Success bool              `json:"success"`
		Data    common.RawMessage `json:"data"`
	}
	if buffered.overflow || common.Unmarshal(buffered.body.Bytes(), &response) != nil || !response.Success {
		common.ApiErrorI18n(c, i18n.MsgAdminTokenOperationFailed)
		return
	}
	if route == "POST /api/channel/" || route == "POST /api/channel/copy/:id" {
		var created struct {
			ID  int   `json:"id,omitempty"`
			IDs []int `json:"ids,omitempty"`
		}
		if common.Unmarshal(response.Data, &created) != nil {
			common.ApiErrorI18n(c, i18n.MsgAdminTokenOperationFailed)
			return
		}
		common.ApiSuccess(c, created)
		return
	}
	if route == "POST /api/channel/multi_key/manage" && action.Scope == "" {
		var status MultiKeyStatusResponse
		if common.Unmarshal(response.Data, &status) == nil {
			for i := range status.Keys {
				status.Keys[i].KeyPreview = ""
				status.Keys[i].Reason = ""
			}
			common.ApiSuccess(c, status)
			return
		}
	}
	common.ApiSuccess(c, gin.H{"channel_ids": action.IDs})
}

// Batch and tag handlers use one transaction with the server-resolved target
// IDs. They never call the unbounded tag helpers used by browser sessions.
func applyAdminTokenChannelBatch(c *gin.Context, action *service.AdminChannelAction) bool {
	updates := map[string]any{}
	daily := model.DailyLimitEdit{}
	switch action.Operation.Action {
	case "POST /api/channel/tag/enabled", "POST /api/channel/tag/disabled", "POST /api/channel/:id/status", "POST /api/channel/status/batch":
		status := common.ChannelStatusEnabled
		if action.Operation.Action == "POST /api/channel/tag/disabled" {
			status = common.ChannelStatusManuallyDisabled
		}
		if strings.Contains(action.Operation.Action, "status") {
			var request ChannelStatusRequest
			if common.Unmarshal(action.Operation.Body, &request) != nil || !isManageableChannelStatus(request.Status) {
				writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
				return true
			}
			status = request.Status
		}
		updates["status"] = status
		updates["daily_limit_disabled_at"] = 0
		updates["daily_limit_disabled_date"] = 0
	case "POST /api/channel/batch/tag":
		var request ChannelBatch
		if common.Unmarshal(action.Operation.Body, &request) != nil {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return true
		}
		updates["tag"] = request.Tag
	case "POST /api/channel/batch/daily_limit":
		var request ChannelDailyLimitBatchRequest
		if common.Unmarshal(action.Operation.Body, &request) != nil {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return true
		}
		daily = request.edit()
	case "PUT /api/channel/tag":
		var request ChannelTag
		if common.Unmarshal(action.Operation.Body, &request) != nil {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return true
		}
		for _, value := range []*string{request.ParamOverride, request.HeaderOverride, request.ModelMapping} {
			if value != nil && strings.TrimSpace(*value) != "" {
				var decoded any
				if common.Unmarshal([]byte(*value), &decoded) != nil {
					writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
					return true
				}
			}
		}
		if request.NewTag != nil {
			updates["tag"] = *request.NewTag
		}
		if request.Models != nil {
			updates["models"] = *request.Models
		}
		if request.Groups != nil {
			updates["group"] = *request.Groups
		}
		if request.Priority != nil {
			updates["priority"] = *request.Priority
		}
		if request.Weight != nil {
			updates["weight"] = *request.Weight
		}
		if request.ModelMapping != nil {
			updates["model_mapping"] = *request.ModelMapping
		}
		if request.ParamOverride != nil {
			updates["param_override"] = *request.ParamOverride
		}
		if request.HeaderOverride != nil {
			updates["header_override"] = *request.HeaderOverride
		}
		daily = request.dailyLimitEdit()
	case "PUT /api/channel/":
		if action.Scope != "" {
			return false
		}
		var request struct {
			Name   *string `json:"name"`
			Remark *string `json:"remark"`
		}
		if common.Unmarshal(action.Operation.Body, &request) != nil {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return true
		}
		if request.Name != nil {
			updates["name"] = *request.Name
		}
		if request.Remark != nil {
			updates["remark"] = *request.Remark
		}
	default:
		return false
	}
	if err := daily.Validate(); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return true
	}
	changes, err := model.UpdateCreatedChannelsWithChanges(c.Request.Context(), c.GetInt("id"), action.IDs, updates, daily)
	if err != nil {
		writeAdminTokenChannelError(c, err)
		return true
	}
	if changes.CacheChanged {
		model.RequestAuditChanges(c).Merge(changes.Audit)
		model.InitChannelCache()
		service.RefreshChannelDailyLimitConfigs()
	}
	if status, ok := updates["status"].(int); ok && status != common.ChannelStatusEnabled {
		closeActiveChannelWebSockets(action.IDs)
	}
	recordManageAudit(c, "channel.token_update", map[string]any{"ids": changes.IDs, "count": len(changes.IDs), "changed": len(changes.IDs) > 0, "changed_fields": changes.Fields, "verification_method": c.GetString("admin_token_verification_method")})
	common.ApiSuccess(c, gin.H{"channel_ids": action.IDs})
	return true
}

type adminTokenResponseWriter struct {
	gin.ResponseWriter
	body     bytes.Buffer
	status   int
	overflow bool
}

func (w *adminTokenResponseWriter) WriteHeader(status int) { w.status = status }
func (w *adminTokenResponseWriter) WriteHeaderNow()        {}
func (w *adminTokenResponseWriter) Status() int            { return w.status }
func (w *adminTokenResponseWriter) Size() int              { return w.body.Len() }
func (w *adminTokenResponseWriter) Written() bool          { return w.body.Len() > 0 }
func (w *adminTokenResponseWriter) Write(data []byte) (int, error) {
	if w.body.Len()+len(data) > 1024*1024 {
		w.overflow = true
		return len(data), nil
	}
	return w.body.Write(data)
}
func (w *adminTokenResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
func (w *adminTokenResponseWriter) Flush() {}

func writeAdminTokenChannelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrChannelNotAccessible):
		common.ApiErrorStatus(c, 403, common.NewMessage(model.ErrChannelNotAccessible.Error()), gin.H{"code": "CHANNEL_NOT_ACCESSIBLE", "message": i18n.T(c, i18n.MsgAdminTokenChannelDenied)})
	case errors.Is(err, service.ErrAdminTokenDenied), errors.Is(err, service.ErrVerificationForbidden):
		common.ApiErrorStatus(c, 403, common.NewMessage(service.ErrAdminTokenDenied.Error()), gin.H{"code": "ADMIN_ACCESS_TOKEN_DISABLED", "message": i18n.T(c, i18n.MsgAdminTokenDenied)})
	case errors.Is(err, service.ErrVerificationContextInvalid):
		common.ApiErrorT(c, "Invalid parameters")
	default:
		logger.LogError(c, "administrator token channel authorization failed: "+err.Error())
		common.ApiErrorStatus(c, 500, common.NewMessage("Please try again later."))
	}
	c.Set("admin_token_denial", true)
	c.Abort()
}

type adminTokenChannelItem struct {
	ID          int     `json:"id"`
	Type        int     `json:"type"`
	Name        string  `json:"name"`
	Status      int     `json:"status"`
	Models      string  `json:"models"`
	Group       string  `json:"group"`
	Tag         *string `json:"tag"`
	Priority    *int64  `json:"priority"`
	Weight      *uint   `json:"weight"`
	CreatedTime int64   `json:"created_time"`
}

func readAdminTokenChannels(c *gin.Context, op service.AdminChannelOperation, ids []int) {
	query := model.ChannelsCreatedBy(model.DB.WithContext(c.Request.Context()), c.GetInt("id"))
	if len(ids) > 0 {
		query = query.Where("channels.id IN ?", ids)
	}
	for key := range op.Query {
		if !slices.Contains([]string{"after_id", "page_size", "keyword", "tag", "type", "status", "model", "group"}, key) {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return
		}
	}
	after, err := strconv.Atoi(c.DefaultQuery("after_id", "0"))
	if err != nil || after < 0 {
		writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || limit < 1 || limit > 100 {
		writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
		return
	}
	query = query.Where("channels.id > ?", after)
	if keyword := op.Query["keyword"]; keyword != "" {
		if len(keyword) > 100 || strings.ContainsAny(keyword, "%_\\") {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return
		}
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ?", id)
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}
	if tag := op.Query["tag"]; tag != "" {
		query = query.Where("tag = ?", tag)
	}
	for _, key := range []string{"type", "status"} {
		if value := op.Query[key]; value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
				return
			}
			query = query.Where(key+" = ?", n)
		}
	}
	if group := op.Query["group"]; group != "" {
		query = model.ApplyChannelGroupFilter(query, group)
	}
	if name := op.Query["model"]; name != "" {
		if strings.ContainsAny(name, "%_\\") {
			writeAdminTokenChannelError(c, service.ErrVerificationContextInvalid)
			return
		}
		query = query.Where("models LIKE ?", "%"+name+"%")
	}
	if op.Action == "GET /api/channel/models_enabled" {
		query = query.Where("status = ?", common.ChannelStatusEnabled)
	}
	items := []adminTokenChannelItem{}
	err = query.Select("id", "type", "name", "status", "models", "group", "tag", "priority", "weight", "created_time").Order("id").Limit(limit + 1).Find(&items).Error
	if err != nil {
		writeAdminTokenChannelError(c, err)
		return
	}
	if len(ids) > 0 {
		if len(items) != 1 {
			writeAdminTokenChannelError(c, model.ErrChannelNotAccessible)
			return
		}
		common.ApiSuccess(c, items[0])
		return
	}
	more := len(items) > limit
	var next *int
	if more {
		items = items[:limit]
		next = &items[len(items)-1].ID
	}
	var result any = items
	switch op.Action {
	case "GET /api/channel/models", "GET /api/channel/models_enabled", "GET /api/channel/tag/models":
		models := []string{}
		for _, channel := range items {
			for _, name := range strings.Split(channel.Models, ",") {
				if name = strings.TrimSpace(name); name != "" {
					models = append(models, name)
				}
			}
		}
		slices.Sort(models)
		result = slices.Compact(models)
	case "GET /api/channel/exclusion-options":
		options := make([]map[string]any, 0, len(items))
		for _, channel := range items {
			options = append(options, map[string]any{"id": channel.ID, "name": channel.Name, "type": channel.Type, "status": channel.Status})
		}
		result = options
	}
	common.ApiSuccess(c, gin.H{"items": result, "next_cursor": next, "has_more": more})
}
