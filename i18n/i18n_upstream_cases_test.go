package i18n

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go/ast"
	"go/parser"
	"go/token"
	"gopkg.in/yaml.v3"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	if err := Init(); err != nil {
		panic("i18n.Init() failed: " + err.Error())
	}
	os.Exit(m.Run())
}

func newTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c
}

func TestInit_LoadsBundleAndLocalizers(t *testing.T) {
	// Already invoked by TestMain; assert the resulting state.
	require.NotNil(t, bundle, "bundle must be constructed")
	require.NotNil(t, localizers[LangEn])
	require.NotNil(t, localizers[LangZhCN])
	require.NotNil(t, localizers[LangZhTW])
	require.NotNil(t, common.TranslateMessage, "Init must wire common.TranslateMessage")
}

func TestInit_IdempotentReturnsNil(t *testing.T) {
	// sync.Once => second call is a no-op and returns the zero (nil) error.
	require.NoError(t, Init())
	require.NoError(t, Init())
}

func TestInit_WiresCommonTranslateMessage(t *testing.T) {
	c := newTestContext()
	// common.TranslateMessage should point at T and resolve real keys.
	got := common.TranslateMessage(c, MsgInvalidParams)
	assert.Equal(t, "Invalid parameters", got)
}

func TestNormalizeLang_Matrix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"zh-TW exact", "zh-TW", LangZhTW},
		{"zh-TW lowercase", "zh-tw", LangZhTW},
		{"zh-TW with region suffix", "zh-tw-hant", LangZhTW},
		{"zh-TW whitespace+case", "  ZH-TW  ", LangZhTW},
		{"zh generic -> zh-CN", "zh", LangZhCN},
		{"zh-CN -> zh-CN", "zh-CN", LangZhCN},
		{"zh-HK uses traditional Chinese", "zh-HK", LangZhTW},
		{"en exact", "en", LangEn},
		{"en-US -> en", "en-US", LangEn},
		{"en uppercase+space", " EN ", LangEn},
		{"unknown -> default", "fr", DefaultLang},
		{"empty -> default", "", DefaultLang},
		{"garbage -> default", "klingon", DefaultLang},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeLang(tc.in))
		})
	}
}

func TestNormalizeLang_ZhTwPrecedesZh(t *testing.T) {
	assert.Equal(t, LangZhTW, normalizeLang("zh-tw"))
	assert.Equal(t, LangZhCN, normalizeLang("zh-cn"))
}

func TestSupportedLanguages(t *testing.T) {
	assert.Equal(t, []string{LangZhCN, LangZhTW, LangEn}, SupportedLanguages())
}

func TestIsSupported_KnownLanguages(t *testing.T) {
	for _, l := range []string{"zh-CN", "zh-TW", "en", "zh", "en-US", "zh-tw"} {
		assert.True(t, IsSupported(l), "expected %q supported", l)
	}
}

func TestIsSupported_UnknownNormalizesToDefaultStillSupported(t *testing.T) {
	assert.True(t, IsSupported("fr"))
	assert.True(t, IsSupported("klingon"))
	assert.True(t, IsSupported(""))
}

func TestParseAcceptLanguage_Matrix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty is unspecified", "", ""},
		{"single tag", "zh-CN", LangZhCN},
		{"first of list wins", "zh-TW,en;q=0.9,zh;q=0.8", LangZhTW},
		{"strips quality value", "en-US;q=0.8", LangEn},
		{"leading whitespace trimmed", "  zh  ,en", LangZhCN},
		{"unknown first -> default", "fr-FR,de", DefaultLang},
		{"quality on first only", "en;q=1.0", LangEn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ParseAcceptLanguage(tc.in))
		})
	}
}

func TestParseAcceptLanguage_LeadingSemicolonNotStripped(t *testing.T) {
	assert.Equal(t, "", ParseAcceptLanguage(";q=0.9"))
}

func TestTranslate_KnownKeyEachLanguage(t *testing.T) {
	assert.Equal(t, "Invalid parameters", Translate(LangEn, MsgInvalidParams))
	assert.Equal(t, "无效的参数", Translate(LangZhCN, MsgInvalidParams))
	assert.Equal(t, "無效的參數", Translate(LangZhTW, MsgInvalidParams))
}

func TestTranslate_UnsupportedLangFallsBackToEnglish(t *testing.T) {
	// "fr" normalizes to en => English text.
	assert.Equal(t, "Invalid parameters", Translate("fr", MsgInvalidParams))
}

func TestTranslate_UnknownKeyReturnsKey(t *testing.T) {
	const missing = "this.key.does.not.exist"
	assert.Equal(t, missing, Translate(LangEn, missing))
	assert.Equal(t, missing, Translate(LangZhCN, missing))
}

func TestTranslate_TemplateDataInterpolated(t *testing.T) {
	got := Translate(LangEn, MsgBatchTooMany, map[string]any{"Max": 20})
	assert.Equal(t, "Too many items in batch request, maximum is 20", got)
}

func TestTranslate_TemplateDataZhCN(t *testing.T) {
	got := Translate(LangZhCN, MsgBatchTooMany, map[string]any{"Max": 5})
	assert.Contains(t, got, "5")
}

func TestTranslate_NilArgsMapSkipsTemplateData(t *testing.T) {
	got := Translate(LangEn, MsgInvalidParams, nil)
	assert.Equal(t, "Invalid parameters", got)
}

func TestTranslate_NoArgs(t *testing.T) {
	assert.Equal(t, "Not found", Translate(LangEn, MsgNotFound))
}

func TestGetLocalizer_KnownLanguagesReturnCached(t *testing.T) {
	assert.Same(t, localizers[LangEn], GetLocalizer("en"))
	assert.Same(t, localizers[LangZhCN], GetLocalizer("zh"))
	assert.Same(t, localizers[LangZhTW], GetLocalizer("zh-TW"))
}

func TestGetLocalizer_CreatesAndCachesWhenMissing(t *testing.T) {
	mu.Lock()
	saved := localizers[LangEn]
	delete(localizers, LangEn)
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		localizers[LangEn] = saved
		mu.Unlock()
	})

	loc := GetLocalizer("en")
	require.NotNil(t, loc, "must create a localizer on cache miss")

	// It must have been written back to the cache...
	mu.RLock()
	cached, ok := localizers[LangEn]
	mu.RUnlock()
	require.True(t, ok, "newly created localizer must be cached")
	assert.Same(t, loc, cached)

	// ...and be usable for translation via the fallback chain.
	assert.Equal(t, "Invalid parameters", Translate("en", MsgInvalidParams))
}

func TestT_UsesLanguageFromContext(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "zh-CN")
	assert.Equal(t, "无效的参数", T(c, MsgInvalidParams))
}

func TestT_NilContextUsesDefault(t *testing.T) {
	assert.Equal(t, "Invalid parameters", T(nil, MsgInvalidParams))
}

func TestT_WithTemplateArgs(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "en")
	got := T(c, MsgBatchTooMany, map[string]any{"Max": 3})
	assert.Equal(t, "Too many items in batch request, maximum is 3", got)
}

func TestGetLangFromContext_NilReturnsDefault(t *testing.T) {
	assert.Equal(t, DefaultLang, GetLangFromContext(nil))
}

func TestGetLangFromContext_UserSettingWins(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "en") // lower priority
	c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: "zh-TW"})
	assert.Equal(t, LangZhTW, GetLangFromContext(c))
}

func TestGetLangFromContext_EmptyUserSettingLangFallsThrough(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: ""})
	c.Set(string(constant.ContextKeyLanguage), "zh-CN")
	assert.Equal(t, LangZhCN, GetLangFromContext(c))
}

func TestGetLangFromContext_UnsupportedUserSettingNormalizesToDefault(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: "fr"})
	assert.Equal(t, DefaultLang, GetLangFromContext(c))
}

func TestGetLangFromContext_LazyLoaderWins(t *testing.T) {
	restore := swapLoader(func(uid int) string {
		if uid == 42 {
			return "zh-TW"
		}
		return ""
	})
	defer restore()

	c := newTestContext()
	c.Set("id", 42)
	c.Set(string(constant.ContextKeyLanguage), "en") // lower priority, must not win
	assert.Equal(t, LangZhTW, GetLangFromContext(c))
}

func TestGetLangFromContext_LoaderEmptyFallsThrough(t *testing.T) {
	restore := swapLoader(func(uid int) string { return "" })
	defer restore()

	c := newTestContext()
	c.Set("id", 7)
	c.Set(string(constant.ContextKeyLanguage), "zh-CN")
	assert.Equal(t, LangZhCN, GetLangFromContext(c))
}

func TestGetLangFromContext_LoaderIdNonPositiveSkipped(t *testing.T) {
	called := false
	restore := swapLoader(func(uid int) string { called = true; return "zh-TW" })
	defer restore()

	c := newTestContext()
	c.Set("id", 0)
	c.Set(string(constant.ContextKeyLanguage), "en")
	assert.Equal(t, LangEn, GetLangFromContext(c))
	assert.False(t, called, "loader must not run for uid<=0")
}

func TestGetLangFromContext_LoaderIdWrongTypeSkipped(t *testing.T) {
	called := false
	restore := swapLoader(func(uid int) string { called = true; return "zh-TW" })
	defer restore()

	c := newTestContext()
	c.Set("id", "not-an-int")
	c.Set(string(constant.ContextKeyLanguage), "zh-CN")
	assert.Equal(t, LangZhCN, GetLangFromContext(c))
	assert.False(t, called)
}

func TestGetLangFromContext_LoaderNoIdSkipped(t *testing.T) {
	called := false
	restore := swapLoader(func(uid int) string { called = true; return "zh-TW" })
	defer restore()

	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "en")
	assert.Equal(t, LangEn, GetLangFromContext(c))
	assert.False(t, called)
}

func TestGetLangFromContext_NilLoaderSkipped(t *testing.T) {
	restore := swapLoader(nil)
	defer restore()

	c := newTestContext()
	c.Set("id", 9)
	c.Set(string(constant.ContextKeyLanguage), "zh-TW")
	assert.Equal(t, LangZhTW, GetLangFromContext(c))
}

func TestGetLangFromContext_ContextLanguage(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "zh-TW")
	assert.Equal(t, LangZhTW, GetLangFromContext(c))
}

func TestGetLangFromContext_AcceptLanguageHeader(t *testing.T) {
	c := newTestContext()
	c.Request.Header.Set("Accept-Language", "zh-CN,en;q=0.8")
	assert.Equal(t, LangZhCN, GetLangFromContext(c))
}

func TestGetLangFromContext_NoSignalsReturnsDefault(t *testing.T) {
	c := newTestContext()
	assert.Equal(t, DefaultLang, GetLangFromContext(c))
}

func TestSetUserLangLoader_InstallsFunc(t *testing.T) {
	restore := swapLoader(nil)
	defer restore()

	SetUserLangLoader(func(uid int) string { return "zh-CN" })
	require.NotNil(t, userLangLoaderFunc)
	assert.Equal(t, "zh-CN", userLangLoaderFunc(1))
}

func swapLoader(fn func(userId int) string) func() {
	prev := userLangLoaderFunc
	userLangLoaderFunc = fn
	return func() { userLangLoaderFunc = prev }
}

func TestTranslate_NilBundleFallsBackToKey(t *testing.T) {
	mu.Lock()
	prevBundle := bundle
	prevLocalizers := localizers
	bundle = nil
	localizers = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		bundle = prevBundle
		localizers = prevLocalizers
		mu.Unlock()
	})

	assert.Nil(t, GetLocalizer("en"))
	assert.Equal(t, "log_export.col.created_at", Translate("en", "log_export.col.created_at"))
	assert.Equal(t, "some.key", Translate("zh-CN", "some.key", map[string]any{"X": 1}))
}

func TestTranslateTwoFALoginExpired(t *testing.T) {
	assert.Equal(t, "Your verification session has expired. Please sign in again.", Translate(LangEn, MsgTwoFALoginExpired))
	assert.Equal(t, "验证会话已过期，请重新登录。", Translate(LangZhCN, MsgTwoFALoginExpired))
	assert.Equal(t, "驗證工作階段已過期，請重新登入。", Translate(LangZhTW, MsgTwoFALoginExpired))
}

func TestLanguageFromUserSetting(t *testing.T) {
	require.NoError(t, Init())
	for _, tc := range []struct {
		saved string
		want  string
	}{
		{"zhCN", LangZhCN},
		{"zhTW", LangZhTW},
		{"zh-HK", LangZhTW},
		{"zh_TW", LangZhTW},
		{"zh_HK", LangZhTW},
		{"zh-Hant-TW", LangZhTW},
		{"zh", LangZhCN},
		{"en", LangEn},
		{"fr", LangEn},
		{"ja", LangEn},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/v1/chat/completions", nil)
		c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: tc.saved})
		assert.Equal(t, tc.want, StatedLang(c), tc.saved)
	}
}

func TestStatedLangLoadsTheUserOnlyWithoutLoadedSettings(t *testing.T) {
	previous := userLangLoaderFunc
	t.Cleanup(func() { userLangLoaderFunc = previous })
	loads := 0
	SetUserLangLoader(func(int) string {
		loads++
		return "zhTW"
	})

	relay, _ := gin.CreateTestContext(httptest.NewRecorder())
	relay.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	relay.Set("id", 7)
	relay.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{})
	assert.Equal(t, "", StatedLang(relay))
	assert.Zero(t, loads)

	dashboard, _ := gin.CreateTestContext(httptest.NewRecorder())
	dashboard.Request = httptest.NewRequest("GET", "/api/user/self", nil)
	dashboard.Set("id", 7)
	assert.Equal(t, LangZhTW, StatedLang(dashboard))
	assert.Equal(t, 1, loads)
}

func TestBackendMessageLanguage(t *testing.T) {
	require.NoError(t, Init())
	t.Cleanup(func() { common.DefaultLanguage = "" })
	quota := map[string]any{"Remaining": "$0.10"}
	const english = "Insufficient account quota. Remaining quota: $0.10. Please top up and try again."
	const chinese = "用户额度不足，剩余额度：$0.10。请充值后重试。"
	for _, tc := range []struct {
		name            string
		defaultLanguage string
		saved           string
		acceptLanguage  string
		want            string
	}{
		{"nothing stated", "", "", "", english},
		{"no preference header", "", "", "*", english},
		{"nothing stated with a Chinese default", "zh-CN", "", "", chinese},
		{"nothing stated with an English default", "en", "", "", english},
		{"header wins over the default", "zh-CN", "", "en-US,en;q=0.9", english},
		{"header in a language without a locale", "zh-CN", "", "fr-FR", english},
		{"saved language wins over the header", "", "zhTW", "en-US", "使用者額度不足，剩餘額度：$0.10。請儲值後重試。"},
		{"saved language", "zh-CN", "en", "", english},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.DefaultLanguage = tc.defaultLanguage
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.acceptLanguage != "" {
				c.Request.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			if tc.saved != "" {
				c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: tc.saved})
			}
			assert.Equal(t, tc.want, T(c, MsgQuotaUserInsufficient, quota))
		})
	}
}

func TestLocalesRenderEveryKey(t *testing.T) {
	require.NoError(t, Init())
	keysFile, err := parser.ParseFile(token.NewFileSet(), "keys.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	var declared []string
	ast.Inspect(keysFile, func(node ast.Node) bool {
		if spec, ok := node.(*ast.ValueSpec); ok {
			for _, value := range spec.Values {
				if literal, ok := value.(*ast.BasicLit); ok {
					key, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					declared = append(declared, key)
				}
			}
		}
		return true
	})
	slices.Sort(declared)

	keysByLang := make(map[string][]string)
	for _, lang := range SupportedLanguages() {
		data, err := localeFS.ReadFile("locales/" + lang + ".yaml")
		require.NoError(t, err)
		var messages map[string]string
		require.NoError(t, yaml.Unmarshal(data, &messages))
		for key := range messages {
			keysByLang[lang] = append(keysByLang[lang], key)
		}
		slices.Sort(keysByLang[lang])
	}
	for _, lang := range SupportedLanguages() {
		assert.Equal(t, declared, keysByLang[lang], lang)
		for _, key := range keysByLang[lang] {
			text := Translate(lang, key, map[string]any{})
			assert.NotEqual(t, key, text, "%s %s", lang, key)
			assert.False(t, strings.Contains(text, "{{"), "%s %s: %s", lang, key, text)
		}
	}
}

func TestWebConsoleMessageResponse(t *testing.T) {
	require.NoError(t, Init())
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "params",
			err:  common.NewMessage("Channel {{name}} (#{{id}}) does not exist", map[string]any{"name": "{{id}}", "id": 7}),
			want: `{"success":false,"message":"Channel {{id}} (#7) does not exist","message_key":"Channel {{name}} (#{{id}}) does not exist","message_params":{"id":7,"name":"{{id}}"}}`,
		},
		{
			name: "no params",
			err:  common.NewMessage("Invalid parameters"),
			want: `{"success":false,"message":"Invalid parameters","message_key":"Invalid parameters"}`,
		},
		{
			name: "plain error",
			err:  errors.New("record not found"),
			want: `{"success":false,"message":"record not found"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/api/channel/7", nil)
			c.Request.Header.Set("Accept-Language", "zh-CN")
			common.ApiError(c, tc.err)
			assert.JSONEq(t, tc.want, recorder.Body.String())
		})
	}
}

func TestServerLogLanguage(t *testing.T) {
	t.Cleanup(func() { common.SetLogLanguage("") })
	const line = "channel #%d has %d unfinished tasks"
	assert.Equal(t, "channel #7 has 2 unfinished tasks", common.LogText(line, 7, 2))
	common.SetLogLanguage("zh-CN")
	assert.Equal(t, "渠道 #7 未完成的任务有: 2", common.LogText(line, 7, 2))
	assert.Equal(t, "a line without a Chinese text: x", common.LogText("a line without a Chinese text: %s", "x"))
	assert.Equal(t, "任务进度轮询开始", common.LogText("task progress polling started"))
	common.SetLogLanguage("en")
	assert.Equal(t, "channel #7 has 2 unfinished tasks", common.LogText(line, 7, 2))
}
