package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLogQueryHandlersPropagateCancellation(t *testing.T) {
	for name, handler := range map[string]gin.HandlerFunc{
		"admin_list": GetAllLogs,
		"user_list":  GetUserLogs,
		"admin_stat": GetLogsStat,
		"user_stat":  GetLogsSelfStat,
		"token_list": GetLogByKey,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/log/", nil).WithContext(ctx)
			c.Set("id", 1)
			c.Set("token_id", 1)
			c.Set("username", "cancelled-query-test")
			handler(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.False(t, decodeResp(t, recorder).Success)
			require.NotContains(t, recorder.Body.String(), "context canceled")
		})
	}
}
