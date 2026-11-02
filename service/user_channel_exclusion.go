package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func UserChannelExclusionPolicy(c *gin.Context) *dto.UserChannelExclusionPolicy {
	if c == nil {
		return nil
	}
	value, _ := c.Get(dto.UserChannelExclusionContextKey)
	policy, _ := value.(*dto.UserChannelExclusionPolicy)
	return policy
}

// CheckUserChannelExclusion is also used by origin-task and locked connection
// paths which do not run the ordinary random selector.
func CheckUserChannelExclusion(c *gin.Context, channelID int, phase string) bool {
	policy := UserChannelExclusionPolicy(c)
	if policy.Allows(channelID) {
		return true
	}
	policy.FailureReason = "user_channel_excluded"
	if policy.Invalid {
		policy.FailureReason = "user_channel_policy_unavailable"
	}
	policy.Phase = phase
	return false
}

func AppendUserChannelRoutingDiagnostic(c *gin.Context, other *model.LogOther) {
	policy := UserChannelExclusionPolicy(c)
	if policy == nil || (!policy.Invalid && policy.RejectedCount == 0) {
		return
	}
	reason := policy.FailureReason
	if reason == "" {
		reason = "candidates_excluded"
	}
	diagnostic := map[string]any{
		"reason": reason, "phase": policy.Phase,
		"excluded_count": len(policy.IDs), "rejected_count": policy.RejectedCount,
		"channel_ids": append([]int{}, policy.RejectedIDs...),
		"truncated":   policy.RejectedCount > len(policy.RejectedIDs),
	}
	if pin, found, _ := GetChannelConstraints(c).ResolvedPin(); found {
		diagnostic["pin_source"] = pin.Source
	}
	other.SetAdmin("user_channel_routing", diagnostic)
}

// Uses the existing bounded asynchronous error-log pipeline. No channel health
// actions, quota mutation, synchronous database write or new goroutine.
func RecordUserChannelRoutingRejection(c *gin.Context, status int, code, summary string) {
	policy := UserChannelExclusionPolicy(c)
	if policy == nil || policy.FailureReason == "" || !constant.ErrorLogEnabled || c.GetBool("user_channel_routing_logged") {
		return
	}
	c.Set("user_channel_routing_logged", true)
	other := model.NewLogOther()
	other.SetPublic("status_code", status)
	other.SetPublic("error_code", code)
	if c.Request != nil && c.Request.URL != nil {
		other.SetPublic("request_path", c.Request.URL.Path)
	}
	AppendUserChannelRoutingDiagnostic(c, other)
	modelName := c.GetString("original_model")
	if modelName == "" {
		// The first distributor rejection precedes channel context setup.
		modelName = policy.ModelName
	}
	model.RecordErrorLog(c, c.GetInt("id"), 0, modelName, c.GetString("token_name"),
		MessageWithCurrentRequestId(c, summary), c.GetInt("token_id"), 0,
		common.GetContextKeyBool(c, constant.ContextKeyIsStream), common.GetContextKeyString(c, constant.ContextKeyUsingGroup), other)
}
