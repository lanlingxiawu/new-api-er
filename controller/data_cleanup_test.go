package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDataCleanupRejectsFinancialAccessBeforeDatabase(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", 1)
		c.Set("role", role)
		c.Request = httptest.NewRequest("POST", "/api/data-cleanup/preview", strings.NewReader(`{"kind":"ledger","before":1}`))
		PreviewDataCleanup(c)
		require.Equal(t, 403, w.Code)
	}
}

func TestDataCleanupRejectsInvalidPreview(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("role", common.RoleRootUser)
	c.Request = httptest.NewRequest("POST", "/api/data-cleanup", strings.NewReader(`{"preview_id":""}`))
	CreateDataCleanup(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"success":false`)
}
