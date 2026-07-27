package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseStatusFilter: equivalence classes over the status query param.
func TestParseStatusFilter(t *testing.T) {
	assert.Equal(t, common.ChannelStatusEnabled, parseStatusFilter("enabled"))
	assert.Equal(t, common.ChannelStatusEnabled, parseStatusFilter("1"))
	assert.Equal(t, 0, parseStatusFilter("disabled"))
	assert.Equal(t, 0, parseStatusFilter("0"))
	assert.Equal(t, -1, parseStatusFilter(""))
	assert.Equal(t, -1, parseStatusFilter("garbage"))
	assert.Equal(t, common.ChannelStatusEnabled, parseStatusFilter("ENABLED"), "case-insensitive")
}

func TestValidateChannelProxy(t *testing.T) {
	tests := []struct {
		name    string
		proxy   string
		wantErr bool
	}{
		{name: "empty"},
		{name: "http", proxy: "http://proxy.example:8080"},
		{name: "https", proxy: "https://proxy.example:8443"},
		{name: "socks5", proxy: "socks5://proxy.example"},
		{name: "socks5h", proxy: "socks5h://proxy.example:1080/"},
		{name: "unsupported", proxy: "ftp://proxy.example", wantErr: true},
		{name: "path", proxy: "socks5://proxy.example:1080/path", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setting, err := common.Marshal(dto.ChannelSettings{Proxy: test.proxy})
			require.NoError(t, err)
			channel := &model.Channel{
				Type:    constant.ChannelTypeOpenAI,
				Setting: common.GetPointer(string(setting)),
			}

			err = validateChannel(channel, false)

			if test.wantErr {
				require.ErrorContains(t, err, "invalid channel proxy")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateChannelRequiresNewAPIBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL *string
		wantErr bool
	}{
		{name: "missing", wantErr: true},
		{name: "blank", baseURL: common.GetPointer("  "), wantErr: true},
		{name: "configured", baseURL: common.GetPointer("https://new-api.example")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel := &model.Channel{
				Type:    constant.ChannelTypeNewAPI,
				BaseURL: test.baseURL,
			}

			err := validateChannel(channel, false)

			if test.wantErr {
				require.ErrorContains(t, err, "New API channel base URL cannot be empty")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNewAPIChannelRegistration(t *testing.T) {
	apiType, ok := common.ChannelType2APIType(constant.ChannelTypeNewAPI)

	require.True(t, ok)
	assert.Equal(t, constant.APITypeNewAPI, apiType)
	assert.Equal(t, "New API", constant.GetChannelTypeName(constant.ChannelTypeNewAPI))
	require.Greater(t, len(constant.ChannelBaseURLs), constant.ChannelTypeNewAPI)
	assert.Empty(t, constant.ChannelBaseURLs[constant.ChannelTypeNewAPI])
}

func TestResponsesCompactAPITypeSupport(t *testing.T) {
	tests := []struct {
		name    string
		apiType int
		want    bool
	}{
		{name: "OpenAI", apiType: constant.APITypeOpenAI, want: true},
		{name: "Codex", apiType: constant.APITypeCodex, want: true},
		{name: "Advanced Custom", apiType: constant.APITypeAdvancedCustom, want: true},
		{name: "Sub2API", apiType: constant.APITypeSub2API, want: true},
		{name: "New API", apiType: constant.APITypeNewAPI, want: true},
		{name: "Anthropic", apiType: constant.APITypeAnthropic, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, common.IsResponsesCompactAPIType(test.apiType))
		})
	}
}

func TestMultiprotocolGatewayEndpointTypes(t *testing.T) {
	want := []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAIResponseCompact,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
		constant.EndpointTypeOpenAIAlphaSearch,
	}

	assert.Equal(t, want, common.GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, "gpt-5"))
	assert.Equal(t, want, common.GetEndpointTypesByChannelType(constant.ChannelTypeSub2API, "gpt-5"))
}

func TestCopyChannelRejectsInvalidLegacyProxySettings(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	settingBytes, err := common.Marshal(dto.ChannelSettings{
		Proxy: "socks5://proxy.example/legacy-path",
	})
	require.NoError(t, err)
	setting := string(settingBytes)
	origin := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    "legacy proxy channel",
		Key:     "test-key",
		Models:  "gpt-test",
		Group:   "default",
		Setting: &setting,
	}
	require.NoError(t, db.Create(origin).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", origin.Id)}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/copy", nil)

	CopyChannel(ctx)

	assert.Contains(t, recorder.Body.String(), "invalid channel settings")
	var channelCount int64
	require.NoError(t, db.Model(&model.Channel{}).Count(&channelCount).Error)
	assert.Equal(t, int64(1), channelCount)
}

func TestDeleteChannelResetsProxyCacheWhenPreReadFails(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	service.ResetProxyClientCache()
	t.Cleanup(service.ResetProxyClientCache)

	proxyURL := "http://proxy.example:8080"
	beforeDelete, err := service.GetHttpClientWithProxy(proxyURL)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "999999"}}
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/channel/999999", nil)

	DeleteChannel(ctx)

	assert.Contains(t, recorder.Body.String(), `"success":true`)
	afterDelete, err := service.GetHttpClientWithProxy(proxyURL)
	require.NoError(t, err)
	assert.NotSame(t, beforeDelete, afterDelete)
}

// selectChannelsForAutomaticTest: manual-disabled channels are always skipped;
// passive-recovery mode restricts to auto-disabled channels only.
func TestSelectChannelsForAutomaticTest(t *testing.T) {
	channels := []*model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusManuallyDisabled},
		{Id: 3, Status: common.ChannelStatusAutoDisabled},
	}

	// scheduled-all mode: keep enabled + auto-disabled, skip manual-disabled
	all := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModeScheduledAll)
	ids := channelIDs(all)
	assert.ElementsMatch(t, []int{1, 3}, ids)

	// passive-recovery mode: only auto-disabled
	passive := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModePassiveRecovery)
	assert.Equal(t, []int{3}, channelIDs(passive))
}

func channelIDs(channels []*model.Channel) []int {
	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	return ids
}

// resolveChannelTestUserID: an authenticated context uses its own user id
// without touching the DB.
func TestResolveChannelTestUserID_UsesRequestUser(t *testing.T) {
	ctx, _ := newCtx(t, http.MethodPost, "/api/channel/test", nil)
	asUser(ctx, 4242)
	id, err := resolveChannelTestUserID(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4242, id)
}

// sanitizeChannelSensitiveSettingsForDisplay masks the account-balance token so
// it never leaks in list/detail responses.
func TestSanitizeChannelSensitiveSettingsForDisplay(t *testing.T) {
	ch := &model.Channel{}
	s := ch.GetSetting()
	s.AccountBalanceToken = "raw-secret-token"
	ch.SetSetting(s)

	sanitizeChannelSensitiveSettingsForDisplay(ch)
	assert.Equal(t, maskedChannelSensitiveToken, ch.GetSetting().AccountBalanceToken)

	// no token -> unchanged (no mask injected)
	empty := &model.Channel{}
	sanitizeChannelSensitiveSettingsForDisplay(empty)
	assert.Empty(t, empty.GetSetting().AccountBalanceToken)
}

// preserveSensitiveChannelSettingsForUpdate: the masked placeholder restores the
// original token; an explicit empty string clears it (so the credential can be
// removed).
func TestPreserveSensitiveChannelSettingsForUpdate(t *testing.T) {
	origin := &model.Channel{}
	os := origin.GetSetting()
	os.AccountBalanceToken = "original-token"
	origin.SetSetting(os)

	// incoming submits the masked placeholder -> original preserved
	masked := &model.Channel{}
	ms := masked.GetSetting()
	ms.AccountBalanceToken = maskedChannelSensitiveToken
	masked.SetSetting(ms)
	preserveSensitiveChannelSettingsForUpdate(masked, origin)
	assert.Equal(t, "original-token", masked.GetSetting().AccountBalanceToken)

	// incoming submits empty string -> token cleared (not restored)
	cleared := &model.Channel{}
	cs := cleared.GetSetting()
	cs.AccountBalanceToken = ""
	cleared.SetSetting(cs)
	preserveSensitiveChannelSettingsForUpdate(cleared, origin)
	assert.Empty(t, cleared.GetSetting().AccountBalanceToken)
}
