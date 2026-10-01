package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// main uses RetryTimes as retries after the initial attempt, not total attempts.
// Compare the actual fork loop helpers with that contract when overrides and
// request-wide limits are disabled; no upstream or database operation is needed.
func TestBranchAuditRetryLoopMatchesMainWithoutOverrides(t *testing.T) {
	withGroupRetryTimes(t, `{}`)
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = false
		s.MaxTotalAttempts = 0
	})
	previous := common.RetryTimes
	t.Cleanup(func() { common.RetryTimes = previous })
	for _, retries := range []int{0, 1, 2, 5, 20} {
		t.Run(fmt.Sprint(retries), func(t *testing.T) {
			common.RetryTimes = retries
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			p := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
			attempts := 0
			for ; ContinueRelayAttempts(c, p); p.IncreaseRetry() {
				BeginRelayAttempt(c, p)
				attempts++
				require.LessOrEqual(t, attempts, retries+1)
				assert.Equal(t, retries-p.GetRetry(), RemainingRetryBudget(c, p))
			}
			assert.Equal(t, retries+1, attempts)
			assert.Equal(t, attempts, p.TotalAttempts())
		})
	}
}
