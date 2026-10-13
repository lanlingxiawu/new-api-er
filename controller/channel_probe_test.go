package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestChannelProbeModeValidation(t *testing.T) {
	for _, settings := range []string{
		`{"probe_request_mode":"unknown"}`,
		`{"probe_request_mode":true}`,
		`{"probe_request_mode":null}`,
		`{"disable_probe_requests":"true"}`,
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(method+settings, func(t *testing.T) {
				channel := map[string]any{"name": "probe-validation", "type": 1, "key": "fixture", "models": "m", "group": "default", "settings": settings}
				var payload any = channel
				if method == http.MethodPost {
					payload = map[string]any{"mode": "single", "channel": channel}
				}
				raw, err := common.Marshal(payload)
				require.NoError(t, err)
				ctx, rec := newRawCtx(t, method, "/api/channel/", string(raw))
				asRoot(ctx, 1)
				if method == http.MethodPost {
					AddChannel(ctx)
				} else {
					UpdateChannel(ctx)
				}
				require.Equal(t, http.StatusOK, rec.Code)
				require.False(t, decodeResp(t, rec).Success)
				require.NotContains(t, rec.Body.String(), "cannot unmarshal")
				require.NotContains(t, rec.Body.String(), "invalid probe")
			})
		}
	}
}
