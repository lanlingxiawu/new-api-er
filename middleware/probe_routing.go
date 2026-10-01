package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/proberouting"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// classifyChannelProbe runs after the existing request parser has installed
// BodyStorage. It must not acquire a new body, read disk, or affect validation.
func classifyChannelProbe(c *gin.Context) {
	policy := model.CurrentChannelProbePolicy()
	if policy == nil || c.Request.Method != http.MethodPost {
		return
	}
	switch c.Request.URL.Path {
	case "/v1/chat/completions", "/v1/responses", "/v1/messages":
	default:
		return
	}
	defer func() {
		if recover() != nil {
			c.Set(service.ProbeRoutingContextKey, (*service.RequestProbeRouting)(nil))
			logger.LogWarn(c, "probe classification failed; using ordinary routing")
		}
	}()
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage.IsDisk() || storage.Size() > proberouting.MaxBodyBytes {
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		return
	}
	chars, matched := proberouting.Classify(c.Request.URL.Path, body, operation_setting.GetProbeRoutingSnapshot().MaxInputChars)
	if matched {
		c.Set(service.ProbeRoutingContextKey, &service.RequestProbeRouting{Policy: policy, InputChars: chars})
	}
}
