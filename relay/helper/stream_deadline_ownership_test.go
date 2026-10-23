package helper

import (
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamDeadlineOwnershipAfterNativeHandoff(t *testing.T) {
	for _, active := range []bool{true, false} {
		name := "disabled_native_handoff"
		if active {
			name = "active_managed_stream"
		}
		t.Run(name, func(t *testing.T) {
			recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/", nil)
			session := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
			c.Writer = relaycommon.NewStreamWriter(c.Writer, session, nil)
			if !active {
				session.Disable() // native Claude owns parsing after this handoff
			}
			ExtendWriteDeadline(c)
			if active {
				require.Empty(t, recorder.deadlines, "managed budget must not be reset externally")
			} else {
				require.Len(t, recorder.deadlines, 1, "passive wrappers must not swallow deadline renewal")
			}
			recorder.deadlines = nil
			SetEventStreamHeaders(c)
			_, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
			require.NoError(t, err)
			require.Len(t, recorder.deadlines, 1, "exactly one writer must own the byte budget")
			require.True(t, recorder.deadlines[0].After(time.Now()))
		})
	}
}
