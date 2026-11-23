package model

import (
	"encoding/json"
	"strconv"

	"github.com/QuantumNous/new-api/common"
)

var ChannelAuditColumns = []string{"id", "name", "type", "key", "status", "weight", "priority", "models", "group", "base_url", "OpenAIOrganization", "test_model", "model_mapping", "status_code_mapping", "auto_ban", "tag", "setting", "settings", "param_override", "header_override", "remark", "other", "channel_info", "daily_quota_limit", "daily_limit_auto_recover", "daily_limit_recover_minutes"}

// Snapshot maps are explicit management write sets. Runtime counters, credential
// hints and authentication caches are never included. Freeze pointer/map values
// so subsequent normalization cannot mutate the old side of a comparison.
func FreezeAuditFields(fields AuditFields) AuditFields {
	if fields == nil {
		return nil
	}
	result := make(AuditFields, len(fields))
	for key, value := range fields {
		encoded, err := common.Marshal(value)
		if err == nil {
			result[key] = json.RawMessage(encoded)
		}
	}
	return result
}

func ChannelAuditSnapshot(channel *Channel) AuditFields {
	if channel == nil {
		return nil
	}
	return FreezeAuditFields(AuditFields{
		"name": channel.Name, "type": channel.Type, "key": channel.Key, "status": channel.Status,
		"weight": channel.Weight, "priority": channel.Priority, "models": channel.Models, "group": channel.Group,
		"base_url": channel.BaseURL, "openai_organization": channel.OpenAIOrganization, "test_model": channel.TestModel,
		"model_mapping": channel.ModelMapping, "status_code_mapping": channel.StatusCodeMapping, "auto_ban": channel.AutoBan,
		"tag": channel.Tag, "setting": channel.Setting, "settings": channel.OtherSettings, "param_override": channel.ParamOverride,
		"header_override": channel.HeaderOverride, "remark": channel.Remark, "other": channel.Other,
		"channel_info": channel.ChannelInfo, "daily_quota_limit": channel.DailyQuotaLimit,
		"daily_limit_auto_recover": channel.DailyLimitAutoRecover, "daily_limit_recover_minutes": channel.DailyLimitRecoverMinutes,
	})
}

func TokenAuditSnapshot(token *Token) AuditFields {
	if token == nil {
		return nil
	}
	return FreezeAuditFields(AuditFields{
		"name": token.Name, "status": token.Status, "expired_time": token.ExpiredTime, "remain_quota": token.RemainQuota,
		"unlimited_quota": token.UnlimitedQuota, "model_limits_enabled": token.ModelLimitsEnabled, "model_limits": token.ModelLimits,
		"allow_ips": token.AllowIps, "allow_user_quota_query": token.AllowUserQuotaQuery, "group": token.Group,
		"cross_group_retry": token.CrossGroupRetry, "auto_groups": token.AutoGroups,
	})
}

func UserAuditSnapshot(user *User) AuditFields {
	if user == nil {
		return nil
	}
	return FreezeAuditFields(AuditFields{
		"username": user.Username, "display_name": user.DisplayName, "email": user.Email, "role": user.Role, "status": user.Status,
		"group": user.Group, "group_ratios": user.GroupRatios, "remark": user.Remark, "setting": user.Setting,
		"channel_exclusions": user.ExcludedChannelIDs, "stream_response_timeout": user.StreamResponseTimeout,
		"stream_response_timeout_mode": user.StreamResponseTimeoutMode, "stream_total_timeout": user.StreamTotalTimeout,
		"non_stream_response_timeout": user.NonStreamResponseTimeout, "non_stream_total_timeout": user.NonStreamTotalTimeout,
		"non_stream_timeout_billing": user.NonStreamTimeoutBilling, "retry_times": user.RetryTimes,
	})
}

func AccessTokenAuditSnapshot(token *UserAccessToken) AuditFields {
	if token == nil {
		return nil
	}
	return FreezeAuditFields(AuditFields{"name": token.Name, "scopes": token.GetScopes(), "expires_at": token.ExpiresAt})
}

func AuditTarget(resource string, id int, name string) AuditTargetChange {
	return AuditTargetChange{Resource: resource, ID: strconv.Itoa(id), Name: name, Visibility: "admin", Consistency: "observed"}
}

func OAuthProviderAuditSnapshot(provider *CustomOAuthProvider) AuditFields {
	if provider == nil {
		return nil
	}
	return FreezeAuditFields(AuditFields{
		"name": provider.Name, "slug": provider.Slug, "icon": provider.Icon, "enabled": provider.Enabled, "client_id": provider.ClientId,
		"client_secret": provider.ClientSecret, "authorization_endpoint": provider.AuthorizationEndpoint, "token_endpoint": provider.TokenEndpoint,
		"user_info_endpoint": provider.UserInfoEndpoint, "scopes": provider.Scopes, "user_id_field": provider.UserIdField,
		"username_field": provider.UsernameField, "display_name_field": provider.DisplayNameField, "email_field": provider.EmailField,
		"well_known": provider.WellKnown, "auth_style": provider.AuthStyle, "access_policy": provider.AccessPolicy, "access_denied_message": provider.AccessDeniedMessage,
	})
}

func SelfAuditTarget(resource string, id, ownerID int, name string) AuditTargetChange {
	target := AuditTarget(resource, id, name)
	target.Visibility, target.OwnerID = "self", ownerID
	return target
}
