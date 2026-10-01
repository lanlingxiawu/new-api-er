package controller

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/i18n"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// zhPriceMonitorPage renders the share page as a zh-CN visitor sees it.
func zhPriceMonitorPage(t *testing.T) string {
	t.Helper()
	require.NoError(t, i18n.Init())
	return renderPriceMonitorPage(i18n.LangZhCN, "/logo.png")
}

var hanCharacters = regexp.MustCompile(`\p{Han}`)

// Rule 13: the whole share page follows one language — static text, script text, dates and
// amounts — so it no longer mixes a Chinese page with header names and tier labels that already
// follow Accept-Language.
func TestPriceMonitorPageRendersWholePageInOneLanguage(t *testing.T) {
	require.NoError(t, i18n.Init())
	for lang, title := range map[string]string{
		i18n.LangEn:   "Model price comparison",
		i18n.LangZhCN: "模型价格对比",
		i18n.LangZhTW: "模型價格對比",
	} {
		t.Run(lang, func(t *testing.T) {
			page := renderPriceMonitorPage(lang, "/logo.png")
			require.Contains(t, page, `<html lang="`+lang+`">`)
			require.Contains(t, page, "<title>"+title+"</title>")
			require.Contains(t, page, "LANG='"+lang+"'", "dates and amounts are formatted in the page language")
			for _, placeholder := range []string{"__T_", "__I18N__", "__LANG__", "__LOGO_SRC__"} {
				require.NotContains(t, page, placeholder)
			}
			require.Contains(t, page, `"invalid_password":`+mustMarshalString(t, i18n.Translate(lang, i18n.MsgPriceMonitorInvalidPassword)))
		})
	}
	english := renderPriceMonitorPage(i18n.LangEn, "/logo.png")
	require.Empty(t, hanCharacters.FindAllString(english, -1), "no Chinese text is left on the English page")
	require.Contains(t, english, `"loaded_of_total":"Loaded {loaded} of {total} models"`, "template arguments become placeholders filled in by the page script")
}

// Every text the template uses has an entry, and every entry is used: a typo would show a raw
// placeholder or "undefined" on the page.
func TestPriceMonitorPageEntriesMatchTemplate(t *testing.T) {
	names := make(map[string]bool, len(priceMonitorPageEntries))
	for _, entry := range priceMonitorPageEntries {
		require.False(t, names[entry.name], "duplicate entry %s", entry.name)
		names[entry.name] = true
		require.True(t,
			strings.Contains(priceMonitorHTML, "__T_"+entry.name+"__") || regexp.MustCompile(`\bT\.`+entry.name+`\b`).MatchString(priceMonitorHTML),
			"entry %s is not used by the page", entry.name)
	}
	for _, match := range regexp.MustCompile(`__T_([a-z0-9_]+)__`).FindAllStringSubmatch(priceMonitorHTML, -1) {
		require.True(t, names[match[1]], "HTML placeholder %s has no entry", match[1])
	}
	for _, match := range regexp.MustCompile(`\bT\.([a-z0-9_]+)`).FindAllStringSubmatch(priceMonitorHTML, -1) {
		require.True(t, names[match[1]], "script text T.%s has no entry", match[1])
	}
}

// Texts are escaped for where they land: HTML-escaped in markup, and the script dictionary cannot
// close the <script> element. The logo URL (an admin setting) is HTML-escaped as before.
func TestPriceMonitorPageEscapesInjectedText(t *testing.T) {
	require.NoError(t, i18n.Init())
	page := renderPriceMonitorPage(i18n.LangEn, `"><script>alert(1)</script>`)
	require.NotContains(t, page, `"><script>alert(1)`)
	require.Contains(t, page, `href="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`)

	encoded, err := marshalPriceMonitorPageText(map[string]string{"x": "</script><b>"})
	require.NoError(t, err)
	require.NotContains(t, encoded, "</script>")
}

// The share page resolves its language exactly like the query API (i18n.GetLangFromContext), and its
// script sends that language back on every query, so both halves of the page always agree.
func TestPriceMonitorViewUsesRequestLanguage(t *testing.T) {
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	for header, want := range map[string]string{
		"en-US,en;q=0.9": i18n.LangEn,
		"zh-TW,zh;q=0.9": i18n.LangZhTW,
		"zh-CN":          i18n.LangZhCN,
		"":               i18n.DefaultLang,
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/price_monitor/view", nil)
		if header != "" {
			c.Request.Header.Set("Accept-Language", header)
		}
		PriceMonitorView(c)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, want, recorder.Header().Get("Content-Language"), header)
		require.Contains(t, recorder.Body.String(), `<html lang="`+want+`">`, header)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	}
	require.Contains(t, priceMonitorHTML, `'Accept-Language':LANG`, "queries are sent in the page language")
}

// The page tells a rejected password from other failures by comparing the API message with the
// invalid-password text it was rendered with; both come from the same key in the same language.
func TestPriceMonitorPagePasswordMessageMatchesQueryAPI(t *testing.T) {
	snapshot := applyPriceSnapshotWithFloor("gpt-4o", completionFloor(3, 6))
	snapshot.AccessPassword = "share123"
	snapshot.PasswordExpireAt = time.Now().Add(time.Hour).Unix()
	useApplyPriceEnv(t, snapshot)
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)

	for _, lang := range i18n.SupportedLanguages() {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/price_monitor/public_query", strings.NewReader(`{"password":"wrong"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("Accept-Language", lang)
		PublicPriceMonitorQuery(c)

		require.Contains(t, recorder.Body.String(), `"message":`+mustMarshalString(t, i18n.Translate(lang, i18n.MsgPriceMonitorInvalidPassword)), lang)
		require.Contains(t, renderPriceMonitorPage(lang, "/logo.png"), `"invalid_password":`+mustMarshalString(t, i18n.Translate(lang, i18n.MsgPriceMonitorInvalidPassword)), lang)
	}
	require.Contains(t, priceMonitorHTML, `failure.passwordRejected=body.message===T.invalid_password`)
	require.Contains(t, priceMonitorHTML, `const passwordError=Boolean(error.passwordRejected)`)
}

func mustMarshalString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := marshalPriceMonitorPageText(value)
	require.NoError(t, err)
	return encoded
}
