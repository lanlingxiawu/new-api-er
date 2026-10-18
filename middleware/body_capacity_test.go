package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDistributorBodyCapacity(t *testing.T) {
	old := common.GetDiskCacheConfig()
	defer common.SetDiskCacheConfig(old)
	common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1})
	for _, path := range []string{"/v1/chat/completions", "/mj/submit/imagine"} {
		for _, contentType := range []string{"application/json", ""} {
			r := gin.New()
			r.Use(BodyStorageCleanup(), Distribute())
			r.POST(path, func(c *gin.Context) { c.Status(204) })
			req := httptest.NewRequest("POST", path, io.NopCloser(strings.NewReader(strings.Repeat("x", 2<<20))))
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 503, w.Code, "%s %s: %s", path, contentType, w.Body.String())
			require.Contains(t, w.Body.String(), "body_cache_capacity")
			require.Zero(t, common.GetBodyMemoryReserved())
		}
	}
}
