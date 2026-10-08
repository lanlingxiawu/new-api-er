package service

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/proberouting"
	"github.com/gin-gonic/gin"
)

const ProbeRoutingContextKey = "channel_probe_routing"

// RequestProbeRouting is request-local. Policy is a pinned immutable snapshot.
type RequestProbeRouting struct {
	Policy     *model.ChannelProbePolicy
	InputChars int
	Group      string
}

func AppendProbeRoutingAdminInfo(c *gin.Context, adminInfo map[string]interface{}) {
	if probe := GetRequestProbeRouting(c); probe != nil {
		adminInfo["probe_routing"] = map[string]interface{}{
			"rule": proberouting.RuleVersion, "input_chars": probe.InputChars,
			"group": probe.Group, "channel_id": c.GetInt("channel_id"),
		}
	}
}

func GetRequestProbeRouting(c *gin.Context) *RequestProbeRouting {
	if c == nil {
		return nil
	}
	value, _ := c.Get(ProbeRoutingContextKey)
	probe, _ := value.(*RequestProbeRouting)
	return probe
}
