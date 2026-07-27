package i18n

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

// TestMain initializes the bundle once for the whole package. Init() is
// guarded by sync.Once, so every function under test operates against a
// fully-loaded bundle exactly as it would in production.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	if err := Init(); err != nil {
		panic("i18n.Init() failed: " + err.Error())
	}
	os.Exit(m.Run())
}

// newTestContext builds a gin context backed by a real (empty) request so
// header lookups (Accept-Language) and c.Get/Set behave like production.
func newTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c
}

// -----------------------------------------------------------------------------
// Init
// -----------------------------------------------------------------------------

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

// -----------------------------------------------------------------------------
// normalizeLang (boundary + equivalence + case/whitespace)
// -----------------------------------------------------------------------------

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
		{"zh-HK falls to zh-CN (zh prefix)", "zh-HK", LangZhCN},
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

// zh-tw must be tested BEFORE the zh prefix (order-sensitive switch).
func TestNormalizeLang_ZhTwPrecedesZh(t *testing.T) {
	assert.Equal(t, LangZhTW, normalizeLang("zh-tw"))
	assert.Equal(t, LangZhCN, normalizeLang("zh-cn"))
}

// -----------------------------------------------------------------------------
// SupportedLanguages / IsSupported
// -----------------------------------------------------------------------------

func TestSupportedLanguages(t *testing.T) {
	assert.Equal(t, []string{LangZhCN, LangZhTW, LangEn}, SupportedLanguages())
}

func TestIsSupported_KnownLanguages(t *testing.T) {
	for _, l := range []string{"zh-CN", "zh-TW", "en", "zh", "en-US", "zh-tw"} {
		assert.True(t, IsSupported(l), "expected %q supported", l)
	}
}

// normalizeLang maps every input to one of the three supported languages, so
// IsSupported returns true even for "unknown" inputs (they normalize to en).
// This documents that the terminal `return false` is unreachable in practice.
func TestIsSupported_UnknownNormalizesToDefaultStillSupported(t *testing.T) {
	assert.True(t, IsSupported("fr"))
	assert.True(t, IsSupported("klingon"))
	assert.True(t, IsSupported(""))
}

// -----------------------------------------------------------------------------
// ParseAcceptLanguage
// -----------------------------------------------------------------------------

func TestParseAcceptLanguage_Matrix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty -> default", "", DefaultLang},
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

// A header that begins with ';' yields idx==0, so the quality-strip branch
// (idx > 0) is skipped and the whole (empty) tag normalizes to default.
func TestParseAcceptLanguage_LeadingSemicolonNotStripped(t *testing.T) {
	assert.Equal(t, DefaultLang, ParseAcceptLanguage(";q=0.9"))
}

// -----------------------------------------------------------------------------
// Translate
// -----------------------------------------------------------------------------

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

// args present but nil map => TemplateData branch skipped; still resolves.
func TestTranslate_NilArgsMapSkipsTemplateData(t *testing.T) {
	got := Translate(LangEn, MsgInvalidParams, nil)
	assert.Equal(t, "Invalid parameters", got)
}

// No args at all => len(args)==0 branch.
func TestTranslate_NoArgs(t *testing.T) {
	assert.Equal(t, "Not found", Translate(LangEn, MsgNotFound))
}

// -----------------------------------------------------------------------------
// GetLocalizer
// -----------------------------------------------------------------------------

func TestGetLocalizer_KnownLanguagesReturnCached(t *testing.T) {
	assert.Same(t, localizers[LangEn], GetLocalizer("en"))
	assert.Same(t, localizers[LangZhCN], GetLocalizer("zh"))
	assert.Same(t, localizers[LangZhTW], GetLocalizer("zh-TW"))
}

// White-box: force the "not yet cached" creation path (lines that build a new
// localizer with a fallback). Normally unreachable because Init pre-creates a
// localizer for every value normalizeLang can return, so we evict one first.
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

// -----------------------------------------------------------------------------
// T (context-driven translate)
// -----------------------------------------------------------------------------

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

// -----------------------------------------------------------------------------
// GetLangFromContext — every priority branch
// -----------------------------------------------------------------------------

func TestGetLangFromContext_NilReturnsDefault(t *testing.T) {
	assert.Equal(t, DefaultLang, GetLangFromContext(nil))
}

// Priority 1: user setting language wins over everything else.
func TestGetLangFromContext_UserSettingWins(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "en") // lower priority
	c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: "zh-TW"})
	assert.Equal(t, LangZhTW, GetLangFromContext(c))
}

// Priority 1 boundary: empty user-setting language is skipped, falls through.
func TestGetLangFromContext_EmptyUserSettingLangFallsThrough(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: ""})
	c.Set(string(constant.ContextKeyLanguage), "zh-CN")
	assert.Equal(t, LangZhCN, GetLangFromContext(c))
}

// Priority 1: an unsupported user-setting language normalizes to en and is
// returned immediately (documents IsSupported totality).
func TestGetLangFromContext_UnsupportedUserSettingNormalizesToDefault(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: "fr"})
	assert.Equal(t, DefaultLang, GetLangFromContext(c))
}

// Priority 2: lazy loader resolves by user id.
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

// Priority 2 skip: loader returns empty => fall through to context language.
func TestGetLangFromContext_LoaderEmptyFallsThrough(t *testing.T) {
	restore := swapLoader(func(uid int) string { return "" })
	defer restore()

	c := newTestContext()
	c.Set("id", 7)
	c.Set(string(constant.ContextKeyLanguage), "zh-CN")
	assert.Equal(t, LangZhCN, GetLangFromContext(c))
}

// Priority 2 skip: id present but non-positive => loader not consulted.
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

// Priority 2 skip: id present but not an int => type assertion fails.
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

// Priority 2 skip: no id in context => loader not consulted.
func TestGetLangFromContext_LoaderNoIdSkipped(t *testing.T) {
	called := false
	restore := swapLoader(func(uid int) string { called = true; return "zh-TW" })
	defer restore()

	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "en")
	assert.Equal(t, LangEn, GetLangFromContext(c))
	assert.False(t, called)
}

// Priority 2 skip: loader is nil entirely => branch guarded out.
func TestGetLangFromContext_NilLoaderSkipped(t *testing.T) {
	restore := swapLoader(nil)
	defer restore()

	c := newTestContext()
	c.Set("id", 9)
	c.Set(string(constant.ContextKeyLanguage), "zh-TW")
	assert.Equal(t, LangZhTW, GetLangFromContext(c))
}

// Priority 3: context language (set by middleware) used when 1 & 2 absent.
func TestGetLangFromContext_ContextLanguage(t *testing.T) {
	c := newTestContext()
	c.Set(string(constant.ContextKeyLanguage), "zh-TW")
	assert.Equal(t, LangZhTW, GetLangFromContext(c))
}

// Priority 4: Accept-Language header used when 1-3 absent.
func TestGetLangFromContext_AcceptLanguageHeader(t *testing.T) {
	c := newTestContext()
	c.Request.Header.Set("Accept-Language", "zh-CN,en;q=0.8")
	assert.Equal(t, LangZhCN, GetLangFromContext(c))
}

// Priority 5: nothing set => default.
func TestGetLangFromContext_NoSignalsReturnsDefault(t *testing.T) {
	c := newTestContext()
	assert.Equal(t, DefaultLang, GetLangFromContext(c))
}

// -----------------------------------------------------------------------------
// SetUserLangLoader
// -----------------------------------------------------------------------------

func TestSetUserLangLoader_InstallsFunc(t *testing.T) {
	restore := swapLoader(nil)
	defer restore()

	SetUserLangLoader(func(uid int) string { return "zh-CN" })
	require.NotNil(t, userLangLoaderFunc)
	assert.Equal(t, "zh-CN", userLangLoaderFunc(1))
}

// swapLoader replaces the package-global loader and returns a restore func so
// tests never leak state into each other.
func swapLoader(fn func(userId int) string) func() {
	prev := userLangLoaderFunc
	userLangLoaderFunc = fn
	return func() { userLangLoaderFunc = prev }
}

// Init() failure is non-fatal for the server (main.go logs and continues), so
// translation must degrade to the key instead of panicking on a nil bundle.
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
