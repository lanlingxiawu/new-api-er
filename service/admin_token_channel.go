package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
)

const (
	VerificationScopeAdminChannelCreate = "admin.channel.create"
	VerificationScopeAdminChannelChange = "admin.channel.change"
	VerificationScopeAdminChannelCopy   = "admin.channel.copy"
	VerificationScopeAdminChannelDelete = "admin.channel.delete"
)

type AdminChannelOperation struct {
	Action    string            `json:"action"`
	ChannelID int               `json:"channel_id,omitempty"`
	Query     map[string]string `json:"query,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
}

type AdminChannelAction struct {
	Operation  AdminChannelOperation
	IDs        []int
	Tag        string
	Scope      string
	Permission authz.Permission
	Sensitive  bool
}

// ParseAdminChannelOperation names only operations with bounded targets. The
// exact body and query are included in the proof; callers cannot add fields
// after verification. Unknown management actions fail closed.
func ParseAdminChannelOperation(op AdminChannelOperation) (*AdminChannelAction, error) {
	if AdminTokenRouteModule(op.Action) != authz.ActionChannelsOwned {
		return nil, ErrVerificationForbidden
	}
	action := &AdminChannelAction{Operation: op, Permission: authz.ChannelRead}
	body := map[string]json.RawMessage{}
	if len(op.Body) > 0 && (common.GetJsonType(op.Body) != "object" || common.Unmarshal(op.Body, &body) != nil) {
		return nil, ErrVerificationContextInvalid
	}
	for key := range body {
		if strings.ToLower(key) != key {
			return nil, ErrVerificationContextInvalid
		}
	}
	if !strings.HasPrefix(op.Action, "GET ") {
		action.Scope = VerificationScopeAdminChannelChange
		action.Permission = authz.ChannelWrite
	}
	switch op.Action {
	case "POST /api/channel/":
		action.Scope = VerificationScopeAdminChannelCreate
		action.Permission = authz.ChannelSensitiveWrite
		var mode string
		var prefix bool
		var channel map[string]json.RawMessage
		if common.Unmarshal(body["mode"], &mode) != nil || !slices.Contains([]string{"single", "batch", "multi_to_single"}, mode) || common.Unmarshal(body["channel"], &channel) != nil {
			return nil, ErrVerificationContextInvalid
		}
		if v := body["batch_add_set_key_prefix_2_name"]; v != nil && (common.Unmarshal(v, &prefix) != nil || prefix) {
			return nil, ErrVerificationForbidden
		}
		if channel["id"] != nil || channel["creator_user_id"] != nil {
			return nil, ErrVerificationContextInvalid
		}
	case "PUT /api/channel/":
		var id int
		if common.Unmarshal(body["id"], &id) != nil {
			return nil, ErrVerificationContextInvalid
		}
		action.IDs = []int{id}
		displayOnly := true
		for key := range body {
			if !slices.Contains([]string{"id", "name", "remark", "type", "key", "base_url", "openai_organization", "test_model", "other", "models", "group", "model_mapping", "status_code_mapping", "priority", "weight", "auto_ban", "tag", "setting", "settings", "param_override", "header_override", "cost_ratio", "daily_quota_limit", "daily_limit_auto_recover", "daily_limit_recover_minutes", "multi_key_mode", "key_mode"}, key) {
				return nil, ErrVerificationContextInvalid
			}
			if !slices.Contains([]string{"id", "name", "remark"}, key) {
				displayOnly = false
			}
			if !slices.Contains([]string{"id", "name", "remark", "models", "group", "model_mapping", "status_code_mapping", "priority", "weight", "auto_ban", "tag", "cost_ratio", "multi_key_mode", "test_model"}, key) {
				action.Sensitive = true
			}
		}
		if displayOnly {
			action.Scope = ""
		}
	case "POST /api/channel/status/batch", "POST /api/channel/batch", "POST /api/channel/batch/tag", "POST /api/channel/batch/daily_limit":
		if common.Unmarshal(body["ids"], &action.IDs) != nil || body["channel_id"] != nil {
			return nil, ErrVerificationContextInvalid
		}
		switch op.Action {
		case "POST /api/channel/batch":
			action.Scope = VerificationScopeAdminChannelDelete
			action.Permission = authz.ChannelSensitiveWrite
		case "POST /api/channel/status/batch":
			action.Permission = authz.ChannelOperate
		case "POST /api/channel/batch/daily_limit":
			action.Permission = authz.ChannelSensitiveWrite
		}
	case "POST /api/channel/tag/enabled", "POST /api/channel/tag/disabled", "PUT /api/channel/tag":
		if common.Unmarshal(body["tag"], &action.Tag) != nil || action.Tag == "" {
			return nil, ErrVerificationContextInvalid
		}
		if op.Action != "PUT /api/channel/tag" {
			action.Permission = authz.ChannelOperate
		} else {
			for _, key := range []string{"param_override", "header_override", "daily_quota_limit", "daily_limit_auto_recover", "daily_limit_recover_minutes"} {
				if body[key] != nil {
					action.Sensitive = true
				}
			}
		}
	case "POST /api/channel/multi_key/manage":
		var id int
		var name string
		if common.Unmarshal(body["channel_id"], &id) != nil || common.Unmarshal(body["action"], &name) != nil {
			return nil, ErrVerificationContextInvalid
		}
		action.IDs = []int{id}
		action.Permission = authz.ChannelOperate
		switch name {
		case "get_key_status":
			action.Scope = ""
		case "disable_key", "enable_key", "enable_all_keys", "disable_all_keys":
		default:
			action.Sensitive = true
		}
	default:
		if strings.Contains(op.Action, ":id") {
			action.IDs = []int{op.ChannelID}
			switch op.Action {
			case "POST /api/channel/copy/:id":
				action.Scope = VerificationScopeAdminChannelCopy
				action.Permission = authz.ChannelSensitiveWrite
			case "DELETE /api/channel/:id":
				action.Scope = VerificationScopeAdminChannelDelete
				action.Permission = authz.ChannelSensitiveWrite
			case "POST /api/channel/:id/status", "GET /api/channel/test/:id", "GET /api/channel/update_balance/:id", "GET /api/channel/fetch_models/:id":
				action.Permission = authz.ChannelOperate
			}
		}
	}
	if len(action.IDs) > 0 {
		slices.Sort(action.IDs)
		action.IDs = slices.Compact(action.IDs)
		if len(action.IDs) > 100 || action.IDs[0] <= 0 {
			return nil, ErrVerificationContextInvalid
		}
	}
	if action.Scope != "" && action.Scope != VerificationScopeAdminChannelCreate && action.Tag == "" && len(action.IDs) == 0 {
		return nil, ErrVerificationContextInvalid
	}
	return action, nil
}

func AuthorizeAdminChannelOperation(ctx context.Context, identity AuthIdentity, op AdminChannelOperation) (*AdminChannelAction, error) {
	action, err := ParseAdminChannelOperation(op)
	if err != nil {
		return nil, err
	}
	tokenID, ok := model.ParseAccessTokenSessionID(identity.SessionID)
	if !ok {
		return nil, ErrVerificationForbidden
	}
	token, err := model.GetUserAccessToken(identity.UserID, tokenID)
	if err != nil {
		return nil, err
	}
	if !AdminTokenLifetimeValid(token) {
		return nil, ErrAdminTokenDenied
	}
	perms := []authz.Permission{authz.AdminMenuChannelsView, action.Permission}
	if action.Sensitive {
		perms = append(perms, authz.ChannelSensitiveWrite)
	}
	_, grants, err := ReadAdminTokenAuthorization(ctx, identity.UserID, authz.ActionChannelsOwned, perms...)
	if err != nil {
		return nil, err
	}
	for _, p := range perms {
		if !grants[p] || (p != authz.AdminMenuChannelsView && !AccessTokenScopeGranted(token.GetScopes(), AccessTokenScopeOf(p))) {
			return nil, ErrVerificationForbidden
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if action.Tag != "" {
		if err := model.ChannelsCreatedBy(model.DB.WithContext(ctx), identity.UserID).Where("tag = ?", action.Tag).Order("id").Limit(101).Pluck("id", &action.IDs).Error; err != nil {
			return nil, err
		}
		if len(action.IDs) == 0 || len(action.IDs) > 100 {
			return nil, model.ErrChannelNotAccessible
		}
	}
	if len(action.IDs) > 0 {
		if err := model.RequireChannelCreator(ctx, identity.UserID, action.IDs); err != nil {
			return nil, err
		}
	}
	return action, nil
}

// BindAuthorizedVerificationOperation checks current permission and object
// ownership before creating a proof binding. Browser-only operations retain
// their existing flow.
func BindAuthorizedVerificationOperation(identity AuthIdentity, op VerificationOperation) (VerificationBinding, error) {
	if strings.HasPrefix(op.Scope, "admin.channel.") {
		var request AdminChannelOperation
		if common.Unmarshal(op.Context, &request) != nil {
			return VerificationBinding{}, ErrVerificationContextInvalid
		}
		action, err := AuthorizeAdminChannelOperation(context.Background(), identity, request)
		if err != nil {
			return VerificationBinding{}, err
		}
		if action.Scope != op.Scope {
			return VerificationBinding{}, ErrVerificationContextInvalid
		}
		// Freeze tag membership in the server-owned digest as well as the body.
		return bindAdminChannelOperation(action)
	}
	return BindVerificationOperation(op)
}

func bindAdminChannelOperation(action *AdminChannelAction) (VerificationBinding, error) {
	// HTTP requests without query parameters and a verification context with
	// query:{} describe the same operation (omitempty drops the empty map).
	if len(action.Operation.Query) == 0 {
		action.Operation.Query = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var channels []model.Channel
	var costs []model.ChannelCostConfig
	if len(action.IDs) > 0 {
		// Exclude counters and timestamps updated by relay and scheduled probes.
		err := model.DB.WithContext(ctx).Select("id", "type", "key", "status", "name", "remark", "OpenAIOrganization", "test_model", "other", "other_info", "tag", "channel_info", "models", "group", "base_url", "setting", "settings", "param_override", "header_override", "model_mapping", "status_code_mapping", "priority", "weight", "auto_ban", "daily_quota_limit", "daily_limit_auto_recover", "daily_limit_recover_minutes").Where("id IN ?", action.IDs).Order("id").Find(&channels).Error
		if err != nil {
			return VerificationBinding{}, err
		}
		if len(channels) != len(action.IDs) {
			return VerificationBinding{}, model.ErrChannelNotAccessible
		}
		for i := range channels {
			channels[i].ChannelInfo.MultiKeyPollingIndex = 0
		}
		if err := model.DB.WithContext(ctx).Select("channel_id", "cost_ratio").Where("channel_id IN ?", action.IDs).Order("channel_id").Find(&costs).Error; err != nil {
			return VerificationBinding{}, err
		}
	}
	var body map[string]json.RawMessage
	if len(action.Operation.Body) > 0 {
		if err := common.Unmarshal(action.Operation.Body, &body); err != nil {
			return VerificationBinding{}, err
		}
	}
	payload, err := common.Marshal(struct {
		Action   string
		ID       int
		Query    map[string]string
		Body     any
		IDs      []int
		Channels []model.Channel
		Costs    []model.ChannelCostConfig
	}{action.Operation.Action, action.Operation.ChannelID, action.Operation.Query, body, action.IDs, channels, costs})
	if err != nil {
		return VerificationBinding{}, err
	}
	return VerificationBinding{Scope: action.Scope, ContextHash: common.GenerateHMACWithKey(authSigningKey("verification-context"), string(payload))}, nil
}
