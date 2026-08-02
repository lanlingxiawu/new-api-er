package controller

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVerify2FALoginInvalidRequestUsesI18n(t *testing.T) {
	tests := []struct {
		name     string
		language string
		message  string
	}{
		{name: "english", language: "en", message: "Invalid parameters"},
		{name: "simplified Chinese", language: "zh-CN", message: "无效的参数"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, rec := newRawCtx(t, http.MethodPost, "/api/user/login/2fa", "{")
			ctx.Request.Header.Set("Accept-Language", test.language)

			Verify2FALogin(ctx)

			response := decodeResp(t, rec)
			assert.False(t, response.Success)
			assert.Equal(t, test.message, response.Message)
		})
	}
}
