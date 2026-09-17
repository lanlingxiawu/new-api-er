package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Legacy ThirdPartySD2 tasks send the channel key only to the channel host;
// output URLs on any other host are fetched without credentials.
func TestThirdPartySD2ContentRequestSendsKeyOnlyToChannelHost(t *testing.T) {
	task := setupGenericTaskTest(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.TaskID+"/content", nil)

	tests := []struct {
		name           string
		platform       string
		resultURL      string
		wantNil        bool
		wantCredential bool
	}{
		{name: "channel host", platform: "58", resultURL: "https://example.com/files/out.mp4", wantCredential: true},
		{name: "channel host with default port", platform: "58", resultURL: "https://EXAMPLE.com:443/files/out.mp4", wantCredential: true},
		{name: "other host", platform: "58", resultURL: "https://cdn.example.net/out.mp4?sig=1"},
		{name: "userinfo naming the channel host", platform: "58", resultURL: "https://example.com@cdn.example.net/out.mp4"},
		{name: "not a legacy SD2 task", platform: "thirdpartysd2", resultURL: "https://example.com/files/out.mp4", wantNil: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			legacy := *task
			legacy.Platform = constant.TaskPlatform(testCase.platform)
			descriptor := thirdPartySD2ContentRequest(c, &legacy, testCase.resultURL)
			if testCase.wantNil {
				assert.Nil(t, descriptor)
				return
			}
			require.NotNil(t, descriptor)
			assert.Equal(t, testCase.resultURL, descriptor.URL)
			assert.Equal(t, http.MethodGet, descriptor.Method)
			if testCase.wantCredential {
				assert.False(t, descriptor.Credentialless)
				assert.Equal(t, map[string]string{"Authorization": "Bearer key"}, descriptor.Headers)
				return
			}
			assert.True(t, descriptor.Credentialless)
			assert.Empty(t, descriptor.Headers)
		})
	}
}
