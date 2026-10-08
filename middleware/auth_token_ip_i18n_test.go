package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Rule 13: the token IP-restriction rejections reach API clients, so they
// follow the request language instead of being hardcoded Chinese.
func TestTokenAuth_IpRestrictionMessagesAreTranslated(t *testing.T) {
	require.NoError(t, i18n.Init())
	user := mkUser(t, nil)
	allowed := "10.9.9.9"
	tk := mkToken(t, user.Id, func(tk *model.Token) {
		tk.UnlimitedQuota = true
		tk.AllowIps = &allowed
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/chat/completions", TokenAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	call := func(remoteAddr, acceptLanguage string) (int, gjson.Result) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.RemoteAddr = remoteAddr
		req.Header.Set("Authorization", "Bearer sk-"+tk.Key)
		if acceptLanguage != "" {
			req.Header.Set("Accept-Language", acceptLanguage)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, gjson.Get(rec.Body.String(), "error")
	}

	code, errBody := call("192.0.2.1:1234", "en")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, string(types.ErrorCodeAccessDenied), errBody.Get("code").String())
	assert.Contains(t, errBody.Get("message").String(), "Your IP address is not in this token's allowed IP list.")

	code, errBody = call("192.0.2.1:1234", "zh-CN")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, errBody.Get("message").String(), "您的 IP 不在令牌允许访问的列表中")

	code, errBody = call("not-an-address", "en")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, errBody.Get("message").String(), "Your client IP address could not be determined")

	code, _ = call("10.9.9.9:1234", "en")
	assert.Equal(t, http.StatusOK, code, "an allowed IP passes")
}
