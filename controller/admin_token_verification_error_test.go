package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminTokenVerificationErrorsDistinguishChannelAccess(t *testing.T) {
	setupAccessTokenAudit(t)
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "channel is not owned", err: model.ErrChannelNotAccessible, code: "CHANNEL_NOT_ACCESSIBLE"},
		{name: "management token is disabled", err: service.ErrAdminTokenDenied, code: "ADMIN_ACCESS_TOKEN_DISABLED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeSecurityOperationError(c, test.err)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"code":"`+test.code+`"`)
			assert.Equal(t, test.code, c.GetString("security_error_code"))
		})
	}
}
