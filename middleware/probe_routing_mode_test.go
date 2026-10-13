package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func probeRoutingDB(t *testing.T) {
	t.Helper()
	if model.DB != nil {
		return
	}
	// Fallback only: portable CRUD and selection SQL. MySQL/PostgreSQL
	// concurrency and locking still require validation on those engines.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	model.InitColumnNames()
	t.Cleanup(func() {
		model.RefreshChannelProbePolicy()
		model.DB = nil
		require.NoError(t, sqlDB.Close())
	})
}

func TestProbeRoutingStreamModes(t *testing.T) {
	probeRoutingDB(t)
	group := uniq("probe-stream")
	oldMemory := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory; model.InitChannelCache() })
	first := mkChannel(t, func(ch *model.Channel) {
		ch.Group, ch.Models = group, group
		ch.Priority = common.GetPointer(int64(100))
	})
	second := mkChannel(t, func(ch *model.Channel) {
		ch.Group, ch.Models = group, group
		ch.Priority = common.GetPointer(int64(0))
	})
	for _, ch := range []*model.Channel{first, second} {
		require.NoError(t, ch.AddAbilities(nil))
		t.Cleanup(func() { require.NoError(t, model.DB.Where("channel_id = ?", ch.Id).Delete(&model.Ability{}).Error) })
	}
	for _, memory := range []bool{false, true} {
		common.MemoryCacheEnabled = memory
		for _, mode := range []string{"all", "non_stream", "stream", ""} {
			for _, enabled := range []bool{false, true} {
				settings := fmt.Sprintf(`{"disable_probe_requests":%t,"probe_request_mode":"%s"}`, enabled, mode)
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", first.Id).Update("settings", settings).Error)
				model.InitChannelCache()
				for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
					for _, streamField := range []string{"", `,"stream":false`, `,"stream":true`} {
						stream := streamField == `,"stream":true`
						blocked := enabled && (mode == "" || mode == "all" || (mode == "stream" && stream) || (mode == "non_stream" && !stream))
						for _, specific := range []bool{false, true} {
							t.Run(fmt.Sprintf("%t/%s/%t/%s/%s/%t", memory, mode, enabled, path, streamField, specific), func(t *testing.T) {
								engine := gin.New()
								engine.Use(func(c *gin.Context) {
									defer common.CleanupBodyStorage(c)
									common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
									if specific {
										common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(first.Id))
									}
									c.Next()
								}, Distribute())
								calls := 0
								engine.POST(path, func(c *gin.Context) {
									calls++
									want := first.Id
									if blocked {
										want = second.Id
									}
									require.Equal(t, want, c.GetInt("channel_id"))
									probe := service.GetRequestProbeRouting(c)
									if blocked {
										require.NotNil(t, probe)
										// Changing the published config must not affect this retry.
										require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", first.Id).Update("settings", `{}`).Error)
										model.RefreshChannelProbePolicy()
										p := &service.RetryParam{Ctx: c, TokenGroup: group, ModelName: group, RequestPath: path, Retry: common.GetPointer(1)}
										selected, actual, err := service.CacheGetRandomSatisfiedChannel(p)
										require.NoError(t, err)
										require.Equal(t, group, actual)
										require.Equal(t, second.Id, selected.Id)
										require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", first.Id).Update("settings", settings).Error)
										model.RefreshChannelProbePolicy()
									} else if probe != nil {
										// Other real DB channels can enable classification globally.
										require.False(t, probe.Policy.Blocks(first.Id))
									}
									c.Status(http.StatusOK)
								})
								input := `"messages":[{"role":"user","content":"hi"}]`
								if path == "/v1/responses" {
									input = `"input":"hi"`
								}
								req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"`+group+`",`+input+streamField+`}`))
								req.Header.Set("Content-Type", "application/json")
								rec := httptest.NewRecorder()
								engine.ServeHTTP(rec, req)
								if specific && blocked {
									require.Equal(t, 403, rec.Code)
									require.Zero(t, calls)
								} else {
									require.Equal(t, 200, rec.Code)
									require.Equal(t, 1, calls)
								}
							})
						}
					}
				}
			}
		}
	}
}
