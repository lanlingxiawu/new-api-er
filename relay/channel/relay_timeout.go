package channel

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// contextualRequestURL is implemented only by adaptors whose URL preparation
// performs an outbound request that must share relay cancellation.
type contextualRequestURL interface {
	GetRequestURLWithContext(c *gin.Context, info *relaycommon.RelayInfo) (string, error)
}

func requestURLForContext(a Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (string, error) {
	if contextual, ok := a.(contextualRequestURL); ok {
		return contextual.GetRequestURLWithContext(c, info)
	}
	return a.GetRequestURL(info)
}
