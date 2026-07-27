package kitutil

// maskHostForURL / maskHostForPlainDomain 原本在主模块 common/str.go，随上游
// 的 relaykit 拆分搬到了本包。测试跟着代码走，从 common/str_test.go 原样移来。

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskHostForURL(t *testing.T) {
	assert.Equal(t, "***", maskHostForURL("localhost"))
	assert.Equal(t, "***.com", maskHostForURL("api.openai.com"))
	assert.Equal(t, "***.co.uk", maskHostForURL("sub.domain.co.uk"))
}

func TestMaskHostForPlainDomain(t *testing.T) {
	assert.Equal(t, "localhost", maskHostForPlainDomain("localhost"))
	assert.Equal(t, "***.com", maskHostForPlainDomain("openai.com"))
	assert.Equal(t, "***.***.com", maskHostForPlainDomain("api.openai.com"))
	assert.Equal(t, "***.***.co.uk", maskHostForPlainDomain("sub.domain.co.uk"))
}

func TestMaskHostTail(t *testing.T) {
	assert.Equal(t, []string{"single"}, maskHostTail([]string{"single"}))
	assert.Equal(t, []string{"com"}, maskHostTail([]string{"openai", "com"}))
	assert.Equal(t, []string{"co", "uk"}, maskHostTail([]string{"domain", "co", "uk"}))
}
