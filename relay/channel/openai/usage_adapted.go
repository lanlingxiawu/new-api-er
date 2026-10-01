package openai

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ApplyUsagePostProcessing fills channel-specific usage fields (cached prompt
// tokens reported outside the standard place) from a response body or the last
// stream frame, as this package's own handlers do. The upstream-stream
// adaptation assembles its response outside this package and needs the same
// step to bill identically.
func ApplyUsagePostProcessing(info *relaycommon.RelayInfo, usage *dto.Usage, responseBody []byte) {
	applyUsagePostProcessing(info, usage, responseBody)
}
