package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/settingsaccess"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apiEnvelope struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

func callJSON(t *testing.T, handler gin.HandlerFunc, method, path, body string) (*gin.Context, apiEnvelope) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	ctx.Set("role", common.RoleRootUser) // the unscoped settings write path is root-only
	handler(ctx)
	var out apiEnvelope
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return ctx, out
}

// An invalid rule set gets a translated message that says what to check, not
// "invalid params" and not a raw Go error.
func TestUpdateOptionGroup_InvalidRelayErrorRules(t *testing.T) {
	before := operation_setting.GetRelayErrorDisplaySetting()
	ctx, out := callJSON(t, UpdateOptionGroup, http.MethodPut, "/api/option/group",
		`{"module":"relay_error_display_setting","values":{"enabled":"true","rules":"[{\"source\":\"upstream\",\"action\":\"replace\"}]"}}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorDisplayInvalid), out.Message)
	assert.NotContains(t, out.Message, "rule 1")
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())
}

func TestRelayErrorDisplayInvalidMessageIsTranslated(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	assert.NotEqual(t, i18n.MsgSettingRelayErrorDisplayInvalid, i18n.T(ctx, i18n.MsgSettingRelayErrorDisplayInvalid))
}

func TestPreviewRelayErrorDisplay(t *testing.T) {
	draft := `"setting":{"enabled":false,"hide_upstream_errors":true,"default_message":"服务暂时不可用","rules":"[{\"name\":\"quota\",\"source\":\"upstream\",\"keywords\":[\"quota\"],\"action\":\"replace\",\"message\":\"服务繁忙\",\"status_code\":503}]"}`

	_, out := callJSON(t, PreviewRelayErrorDisplay, http.MethodPost, "/api/option/relay-error-display/preview",
		`{`+draft+`,"sample":{"source":"upstream","status_code":403,"error_code":"pre_consume_token_quota_failed","message":"token quota is not enough"}}`)
	require.True(t, out.Success, out.Message)
	assert.Equal(t, true, out.Data["replace"], "previewed as if enabled, so the draft can be checked before switching it on")
	assert.Equal(t, "服务繁忙", out.Data["message"])
	assert.EqualValues(t, 503, out.Data["status_code"])
	assert.EqualValues(t, 0, out.Data["rule_index"])
	assert.Equal(t, "quota", out.Data["rule_name"])

	_, out = callJSON(t, PreviewRelayErrorDisplay, http.MethodPost, "/api/option/relay-error-display/preview",
		`{`+draft+`,"sample":{"source":"local","status_code":400,"error_code":"invalid_request","message":"field messages is required"}}`)
	require.True(t, out.Success, out.Message)
	assert.Equal(t, false, out.Data["replace"])
	assert.Equal(t, "field messages is required", out.Data["message"], "unchanged errors are shown as the user would see them")
	assert.EqualValues(t, 400, out.Data["status_code"])

	_, out = callJSON(t, PreviewRelayErrorDisplay, http.MethodPost, "/api/option/relay-error-display/preview",
		`{`+draft+`,"sample":{"source":"cloud","message":"x"}}`)
	assert.False(t, out.Success)

	ctx, out := callJSON(t, PreviewRelayErrorDisplay, http.MethodPost, "/api/option/relay-error-display/preview",
		`{"setting":{"rules":"{"},"sample":{"source":"upstream","message":"x"}}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorDisplayInvalid), out.Message)
}

func TestGetRelayErrorDisplayPresets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/option/relay-error-display/presets", nil)
	ctx.Set("role", common.RoleRootUser)
	GetRelayErrorDisplayPresets(ctx)
	var out struct {
		Success bool                               `json:"success"`
		Data    []operation_setting.RelayErrorRule `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	require.True(t, out.Success)
	assert.Equal(t, operation_setting.RelayErrorPresetRules(i18n.GetLangFromContext(ctx)), out.Data)
}

// The UI passes its own language, which the presets follow; languages the
// backend has no translations for get English.
func TestGetRelayErrorDisplayPresets_UILanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for lang, want := range map[string]string{"zh": i18n.LangZhCN, "zh-TW": i18n.LangZhTW, "en": i18n.LangEn, "fr": i18n.LangEn} {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/option/relay-error-display/presets?lang="+lang, nil)
		ctx.Request.Header.Set("Accept-Language", "zh-CN")
		ctx.Set("role", common.RoleRootUser)
		GetRelayErrorDisplayPresets(ctx)
		var out struct {
			Data []operation_setting.RelayErrorRule `json:"data"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, operation_setting.RelayErrorPresetRules(want), out.Data, "lang=%s", lang)
	}
}

// A find-and-replace step that cannot be saved is named, so the admin knows
// which rule and step to fix.
func TestRelayErrorEditInvalidNamesRuleAndStep(t *testing.T) {
	rules := `[{\"source\":\"any\",\"action\":\"keep\"},{\"source\":\"any\",\"action\":\"edit\",\"edits\":[{\"find\":\"x\"},{\"find\":\"a*\",\"regex\":true}]}]`
	ctx, out := callJSON(t, PreviewRelayErrorDisplay, http.MethodPost, "/api/option/relay-error-display/preview",
		`{"setting":{"rules":"`+rules+`"},"sample":{"source":"upstream","message":"x"}}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorEditInvalid, map[string]any{"Rule": 2, "Step": 2}), out.Message)
	assert.Contains(t, out.Message, "2")

	before := operation_setting.GetRelayErrorDisplaySetting()
	ctx, out = callJSON(t, UpdateOptionGroup, http.MethodPut, "/api/option/group",
		`{"module":"relay_error_display_setting","values":{"enabled":"true","rules":"`+rules+`"}}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorEditInvalid, map[string]any{"Rule": 2, "Step": 2}), out.Message)
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())
}

// A regular expression too costly for the error path is refused with its own
// advice (smaller repeat counts), naming the rule and step, on save and preview.
func TestRelayErrorEditTooComplexNamesRuleAndStep(t *testing.T) {
	pattern := strings.Repeat(`.{1,999}`, 24) + "Q"
	rules := `[{\"source\":\"any\",\"action\":\"edit\",\"edits\":[{\"find\":\"` + pattern + `\",\"regex\":true}]}]`
	ctx, out := callJSON(t, PreviewRelayErrorDisplay, http.MethodPost, "/api/option/relay-error-display/preview",
		`{"setting":{"rules":"`+rules+`"},"sample":{"source":"upstream","message":"x"}}`)
	assert.False(t, out.Success)
	want := i18n.T(ctx, i18n.MsgSettingRelayErrorEditTooComplex, map[string]any{"Rule": 1, "Step": 1})
	assert.NotEqual(t, i18n.MsgSettingRelayErrorEditTooComplex, want, "translated")
	assert.Equal(t, want, out.Message)

	before := operation_setting.GetRelayErrorDisplaySetting()
	ctx, out = callJSON(t, UpdateOptionGroup, http.MethodPut, "/api/option/group",
		`{"module":"relay_error_display_setting","values":{"enabled":"true","rules":"`+rules+`"}}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorEditTooComplex, map[string]any{"Rule": 1, "Step": 1}), out.Message)
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())
}

// Review item 5: RequireSystemSettingsScope checks the permission of whatever
// scope the request names, so the handlers must act only for this feature's
// scope (or root's unrestricted ""); an admin allowed to view another settings
// scope must not read status, presets or previews.
func TestRelayErrorDisplayHandlersRequireTheirOwnScope(t *testing.T) {
	require.NoError(t, i18n.Init())
	preview := `{"setting":{"enabled":true,"hide_upstream_errors":true},"sample":{"source":"upstream","message":"x"}}`
	handlers := []struct {
		name    string
		handler gin.HandlerFunc
		method  string
		body    string
	}{
		{"status", GetRelayErrorDisplayStatus, http.MethodGet, ""},
		{"presets", GetRelayErrorDisplayPresets, http.MethodGet, ""},
		{"preview", PreviewRelayErrorDisplay, http.MethodPost, preview},
	}
	for _, tc := range []struct {
		name     string
		role     int
		scope    any // nil: no scope recorded (no middleware in front)
		accepted bool
	}{
		{"own scope", common.RoleAdminUser, settingsaccess.ScopeRelayErrorDisplay, true},
		{"another scope", common.RoleAdminUser, "site.notice", false},
		{"root unrestricted", common.RoleRootUser, "", true},
		{"admin with an empty scope", common.RoleAdminUser, "", false},
		{"not a string", common.RoleRootUser, 1, false},
		{"direct call by root", common.RoleRootUser, nil, true},
		{"direct call by admin", common.RoleAdminUser, nil, false},
	} {
		for _, h := range handlers {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(h.method, "/api/option/relay-error-display/"+h.name, strings.NewReader(h.body))
			ctx.Set("role", tc.role)
			if tc.scope != nil {
				ctx.Set(middleware.SystemSettingsScopeContextKey, tc.scope)
			}
			h.handler(ctx)
			var success struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &success), rec.Body.String())
			assert.Equal(t, tc.accepted, success.Success, "%s: %s", tc.name, h.name)
			if !tc.accepted {
				assert.Equal(t, i18n.T(ctx, i18n.MsgInvalidParams), success.Message, "%s: %s", tc.name, h.name)
			}
		}
	}
}

// Review item 6: PUT /api/option/ with a relay_error_display_setting key was
// saved without validating the rules. It is refused with the same message as
// the group save, and nothing changes.
func TestUpdateOption_InvalidRelayErrorRules(t *testing.T) {
	require.NoError(t, i18n.Init())
	before := operation_setting.GetRelayErrorDisplaySetting()
	ctx, out := callJSON(t, UpdateOption, http.MethodPut, "/api/option/",
		`{"key":"relay_error_display_setting.rules","value":"[{\"source\":\"upstream\",\"action\":\"replace\"}]"}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorDisplayInvalid), out.Message)
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())

	rules := `[{\"source\":\"any\",\"action\":\"edit\",\"edits\":[{\"find\":\"x\"},{\"find\":\"a*\",\"regex\":true}]}]`
	ctx, out = callJSON(t, UpdateOption, http.MethodPut, "/api/option/",
		`{"key":"relay_error_display_setting.rules","value":"`+rules+`"}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorEditInvalid, map[string]any{"Rule": 1, "Step": 2}), out.Message)
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())
}

// A bad field name or value on the single-option endpoint gets the translated
// message too, never a Go error string.
func TestUpdateOption_InvalidRelayErrorFieldIsTranslated(t *testing.T) {
	require.NoError(t, i18n.Init())
	before := operation_setting.GetRelayErrorDisplaySetting()
	for _, body := range []string{
		`{"key":"relay_error_display_setting.enabled","value":"maybe"}`,
		`{"key":"relay_error_display_setting.unknown","value":"x"}`,
	} {
		ctx, out := callJSON(t, UpdateOption, http.MethodPut, "/api/option/", body)
		assert.False(t, out.Success, body)
		assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorDisplayInvalid), out.Message, body)
	}
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())
}
