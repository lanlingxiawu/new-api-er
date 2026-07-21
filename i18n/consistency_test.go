package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// loadLocale parses one embedded locale file into a flat message-id -> text map.
func loadLocale(t *testing.T, file string) map[string]string {
	t.Helper()
	raw, err := localeFS.ReadFile(file)
	require.NoError(t, err, "read embedded %s", file)

	m := make(map[string]string)
	require.NoError(t, yaml.Unmarshal(raw, &m), "unmarshal %s", file)
	return m
}

// constantKeys extracts the string value of every Msg* constant declared in
// keys.go via the Go AST, so the test stays in lock-step with the source
// without hand-maintaining a 249-entry list.
func constantKeys(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "keys.go", nil, 0)
	require.NoError(t, err, "parse keys.go")

	out := make(map[string]string)
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out[name.Name] = val
			}
		}
	}
	require.NotEmpty(t, out, "keys.go should declare message constants")
	return out
}

// Every locale file must expose exactly the same set of message ids.
func TestLocales_KeySetsIdentical(t *testing.T) {
	en := loadLocale(t, "locales/en.yaml")
	zhCN := loadLocale(t, "locales/zh-CN.yaml")
	zhTW := loadLocale(t, "locales/zh-TW.yaml")

	enKeys := sortedKeys(en)
	assert.Equal(t, enKeys, sortedKeys(zhCN), "en vs zh-CN key set mismatch")
	assert.Equal(t, enKeys, sortedKeys(zhTW), "en vs zh-TW key set mismatch")
}

// No empty translation values in any locale.
func TestLocales_NoEmptyValues(t *testing.T) {
	for _, file := range []string{"locales/en.yaml", "locales/zh-CN.yaml", "locales/zh-TW.yaml"} {
		m := loadLocale(t, file)
		for k, v := range m {
			assert.NotEmpty(t, v, "%s: empty translation for %q", file, k)
		}
	}
}

// Rule 13: every message-key constant must resolve to a non-empty translation
// in EVERY loaded language (en source of truth, zh-CN, zh-TW).
func TestConstants_ResolveInAllLocales(t *testing.T) {
	consts := constantKeys(t)
	locales := map[string]map[string]string{
		"en":    loadLocale(t, "locales/en.yaml"),
		"zh-CN": loadLocale(t, "locales/zh-CN.yaml"),
		"zh-TW": loadLocale(t, "locales/zh-TW.yaml"),
	}

	for constName, key := range consts {
		for lang, m := range locales {
			val, ok := m[key]
			assert.Truef(t, ok, "constant %s (%q) missing from %s locale", constName, key, lang)
			assert.NotEmptyf(t, val, "constant %s (%q) empty in %s locale", constName, key, lang)
		}
	}
}

// Cross-check: every constant also resolves through the live Translate path
// (not just raw YAML) so the go-i18n bundle actually serves the value.
func TestConstants_ResolveThroughTranslate(t *testing.T) {
	consts := constantKeys(t)
	for _, lang := range SupportedLanguages() {
		for constName, key := range consts {
			got := Translate(lang, key)
			assert.NotEqualf(t, key, got,
				"constant %s (%q) did not translate in %s (returned the key itself)", constName, key, lang)
			assert.NotEmptyf(t, got, "constant %s (%q) translated to empty in %s", constName, key, lang)
		}
	}
}

// Guard against orphaned locale entries: every YAML key should be backed by a
// declared constant (keeps keys.go and the locale files in sync).
func TestLocales_NoOrphanKeys(t *testing.T) {
	consts := constantKeys(t)
	declared := make(map[string]bool, len(consts))
	for _, key := range consts {
		declared[key] = true
	}

	en := loadLocale(t, "locales/en.yaml")
	for key := range en {
		assert.Truef(t, declared[key], "locale key %q has no corresponding Msg* constant", key)
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
