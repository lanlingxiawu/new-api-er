package common

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestPageInfoAccessors(t *testing.T) {
	p := &PageInfo{Page: 3, PageSize: 20}
	assert.Equal(t, 40, p.GetStartIdx()) // (3-1)*20
	assert.Equal(t, 60, p.GetEndIdx())   // 3*20
	assert.Equal(t, 20, p.GetPageSize())
	assert.Equal(t, 3, p.GetPage())

	p.SetTotal(99)
	assert.Equal(t, 99, p.Total)
	p.SetItems([]int{1, 2})
	assert.Equal(t, []int{1, 2}, p.Items)
}

func pageCtx(t *testing.T, rawQuery string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?"+rawQuery, nil)
	return c
}

func TestGetPageQuery(t *testing.T) {
	origItems := ItemsPerPage
	t.Cleanup(func() { ItemsPerPage = origItems })
	ItemsPerPage = 10

	t.Run("explicit page and page_size", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, "p=2&page_size=20"))
		assert.Equal(t, 2, p.Page)
		assert.Equal(t, 20, p.PageSize)
	})

	t.Run("defaults when absent", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, ""))
		assert.Equal(t, 1, p.Page)
		assert.Equal(t, 10, p.PageSize) // ItemsPerPage fallback
	})

	t.Run("page zero normalized to one", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, "p=0&page_size=5"))
		assert.Equal(t, 1, p.Page)
		assert.Equal(t, 5, p.PageSize)
	})

	t.Run("page size clamped to 100", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, "p=1&page_size=500"))
		assert.Equal(t, 100, p.PageSize)
	})

	t.Run("ps fallback", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, "p=1&ps=15"))
		assert.Equal(t, 15, p.PageSize)
	})

	t.Run("size fallback", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, "p=1&size=25"))
		assert.Equal(t, 25, p.PageSize)
	})

	t.Run("boundary page size 100 kept", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, "p=1&page_size=100"))
		assert.Equal(t, 100, p.PageSize)
	})

	t.Run("negative page preserved via compat path", func(t *testing.T) {
		p := GetPageQuery(pageCtx(t, "p=-5&page_size=10"))
		assert.Equal(t, -5, p.Page)
	})
}
