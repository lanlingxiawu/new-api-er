package helper

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// copyCodexSSEHeaders
// ---------------------------------------------------------------------------

func TestCopyCodexSSEHeaders(t *testing.T) {
	t.Run("copies configured codex headers, skips empty values", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		resp := &http.Response{Header: http.Header{}}
		resp.Header.Add("X-Reasoning-Included", "true")
		resp.Header.Add("X-Codex-Turn-State", "") // empty value skipped
		resp.Header.Add("X-Codex-Turn-State", "state1")

		copyCodexSSEHeaders(c, resp)
		require.Equal(t, "true", c.Writer.Header().Get("X-Reasoning-Included"))
		require.Equal(t, []string{"state1"}, c.Writer.Header().Values("X-Codex-Turn-State"))
	})

	t.Run("nil guards", func(t *testing.T) {
		require.NotPanics(t, func() { copyCodexSSEHeaders(nil, nil) })
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		require.NotPanics(t, func() { copyCodexSSEHeaders(c, nil) })
	})

	// Sanity: the header name we skip via ShouldCopyUpstreamHeader logic is honored.
	require.False(t, service.ShouldCopyUpstreamHeader(nil, "Content-Length", []string{"5"}))
}

// ---------------------------------------------------------------------------
// ModelPriceHelperPerCall — overflow on both branches
// ---------------------------------------------------------------------------

func TestModelPriceHelperPerCall_Overflow(t *testing.T) {
	snapshotRatioSettings(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	t.Run("price branch overflow rejected", func(t *testing.T) {
		huge := float64(common.MaxQuota)/common.QuotaPerUnit + 100
		m, err := common.Marshal(map[string]float64{"percall-overflow-price": huge})
		require.NoError(t, err)
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(m)))
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "percall-overflow-price"
		_, err = ModelPriceHelperPerCall(ctx, info)
		var clamp *common.QuotaClamp
		require.ErrorAs(t, err, &clamp)
		require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	})

	t.Run("ratio branch overflow rejected", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"percall-overflow-ratio":100000000000000}`))
		ctx, info, _ := newPriceContext("default")
		info.OriginModelName = "percall-overflow-ratio"
		_, err := ModelPriceHelperPerCall(ctx, info)
		var clamp *common.QuotaClamp
		require.ErrorAs(t, err, &clamp)
		require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	})
}

// ---------------------------------------------------------------------------
// modelPriceNotConfiguredError — admin vs non-admin (admin path needs DB)
// ---------------------------------------------------------------------------

func TestModelPriceNotConfiguredError_NonAdmin(t *testing.T) {
	// userId 0 short-circuits IsAdmin without a DB call.
	err := modelPriceNotConfiguredError("some-model", 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "some-model")
	require.Contains(t, err.Error(), "contact the site administrator")
}

func findProjectRootHelper() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// connectMainDB best-effort connects to the real MySQL/PG main DB via .env.
// Returns nil (and the test skips) when unreachable.
func connectMainDB(t *testing.T) *gorm.DB {
	t.Helper()
	root := findProjectRootHelper()
	if root != "" {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
	dsn := os.Getenv("SQL_DSN")
	if dsn == "" {
		t.Skip("SQL_DSN not set; skipping DB-gated admin error test")
	}
	var db *gorm.DB
	var err error
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
		db, err = gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	} else {
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
		if !strings.Contains(dsn, "parseTime") {
			if strings.Contains(dsn, "?") {
				dsn += "&parseTime=true"
			} else {
				dsn += "?parseTime=true"
			}
		}
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	}
	if err != nil {
		t.Skipf("cannot reach main DB: %v", err)
	}
	return db
}

func TestModelPriceNotConfiguredError_Admin(t *testing.T) {
	db := connectMainDB(t)
	prev := model.DB
	model.DB = db
	model.InitColumnNames()
	t.Cleanup(func() { model.DB = prev })

	require.NoError(t, db.AutoMigrate(&model.User{}))

	const adminID = 990099
	// Seed an admin user with a unique id, clean up afterwards.
	db.Unscoped().Where("id = ?", adminID).Delete(&model.User{})
	admin := &model.User{Id: adminID, Username: "helper-admin-test", Role: common.RoleAdminUser}
	require.NoError(t, db.Create(admin).Error)
	t.Cleanup(func() { db.Unscoped().Where("id = ?", adminID).Delete(&model.User{}) })

	require.True(t, model.IsAdmin(adminID))
	err := modelPriceNotConfiguredError("admin-model", adminID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "admin-model")
	require.Contains(t, err.Error(), "self-use mode")

	// Reach the admin branch through ModelPriceHelper too.
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "admin-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		UserId:          adminID,
	}
	_, err = ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "self-use mode")
}
