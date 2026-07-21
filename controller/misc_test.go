package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetNotice / GetAbout surface the corresponding OptionMap entries verbatim.
func TestGetNotice(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	prev, had := common.OptionMap["Notice"]
	common.OptionMap["Notice"] = "hello-notice"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["Notice"] = prev
		} else {
			delete(common.OptionMap, "Notice")
		}
		common.OptionMapRWMutex.Unlock()
	})

	ctx, rec := newCtx(t, http.MethodGet, "/api/notice", nil)
	GetNotice(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var data string
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	assert.Equal(t, "hello-notice", data)
}

func TestGetAbout(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	prev, had := common.OptionMap["About"]
	common.OptionMap["About"] = "about-text"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["About"] = prev
		} else {
			delete(common.OptionMap, "About")
		}
		common.OptionMapRWMutex.Unlock()
	})

	ctx, rec := newCtx(t, http.MethodGet, "/api/about", nil)
	GetAbout(ctx)
	resp := decodeResp(t, rec)
	require.True(t, resp.Success, resp.Message)
	var data string
	require.NoError(t, common.Unmarshal(resp.Data, &data))
	assert.Equal(t, "about-text", data)
}

// GetUserAgreement / GetPrivacyPolicy always succeed and return the legal text.
func TestGetLegalPages(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/user_agreement", nil)
	GetUserAgreement(ctx)
	assert.True(t, decodeResp(t, rec).Success)

	ctx2, rec2 := newCtx(t, http.MethodGet, "/api/privacy_policy", nil)
	GetPrivacyPolicy(ctx2)
	assert.True(t, decodeResp(t, rec2).Success)
}
