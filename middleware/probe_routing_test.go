package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the distribution boundary against real configured DB rows and local
// upstreams, without invoking real AI APIs or reproducing the billing hot path.
func TestProbeRoutingDistribution(t *testing.T) {
	requireDB(t)
	group, modelName := uniq("probe-group"), uniq("probe-model")
	oldMemory := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory; model.InitChannelCache() })
	var forbiddenCalls, allowedCalls atomic.Int32
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forbiddenCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer forbidden.Close()
	const userBody = `{"input":[{"role":"user","content":"在吗"}],"instructions":"只回复一个字:好","max_output_tokens":8296}`
	var received atomic.Value
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedCalls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		received.Store(string(raw))
		_, _ = w.Write([]byte(`{"output_text":"好"}`))
	}))
	defer allowed.Close()
	first := mkChannel(t, func(ch *model.Channel) {
		ch.Group = group
		ch.Models = modelName
		ch.BaseURL = common.GetPointer(forbidden.URL)
		ch.OtherSettings = `{"disable_probe_requests":true}`
		ch.Priority = common.GetPointer(int64(100))
	})
	second := mkChannel(t, func(ch *model.Channel) {
		ch.Group = group
		ch.Models = modelName
		ch.BaseURL = common.GetPointer(allowed.URL)
		ch.Priority = common.GetPointer(int64(0))
	})
	for _, ch := range []*model.Channel{first, second} {
		require.NoError(t, ch.AddAbilities(nil))
		t.Cleanup(func() { model.DB.Where("channel_id = ?", ch.Id).Delete(&model.Ability{}) })
	}
	body := strings.Replace(userBody, `{"input"`, `{"model":"`+modelName+`","input"`, 1)
	for _, memory := range []bool{false, true} {
		t.Run(strconv.FormatBool(memory), func(t *testing.T) {
			common.MemoryCacheEnabled = memory
			model.InitChannelCache()
			var specific string
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				defer common.CleanupBodyStorage(c)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
				if specific != "" {
					common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, specific)
				}
				c.Next()
			}, Distribute())
			engine.POST("/v1/responses", func(c *gin.Context) {
				defer common.CleanupBodyStorage(c)
				require.Equal(t, second.Id, c.GetInt("channel_id"))
				probe := service.GetRequestProbeRouting(c)
				require.NotNil(t, probe)
				// Pinning survives retries without consuming attempts on filtering.
				p := &service.RetryParam{Ctx: c, TokenGroup: group, ModelName: modelName, RequestPath: "/v1/responses", Retry: common.GetPointer(2)}
				selected, actualGroup, err := service.CacheGetRandomSatisfiedChannel(p)
				require.NoError(t, err)
				require.Equal(t, group, actualGroup)
				require.Equal(t, second.Id, selected.Id)
				require.Zero(t, p.TotalAttempts())
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				raw, err := storage.Bytes()
				require.NoError(t, err)
				resp, err := http.Post(allowed.URL, "application/json", strings.NewReader(string(raw)))
				require.NoError(t, err)
				defer resp.Body.Close()
				c.Status(resp.StatusCode)
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)
			require.Equal(t, 200, rec.Code)
			require.Equal(t, body, received.Load())
			require.Zero(t, forbiddenCalls.Load())
			specific = strconv.Itoa(first.Id)
			rec = httptest.NewRecorder()
			req = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)
			require.Equal(t, 403, rec.Code)
			specific = ""
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", second.Id).Update("settings", `{"disable_probe_requests":true}`).Error)
			model.RefreshChannelProbePolicy()
			rec = httptest.NewRecorder()
			req = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)
			require.Equal(t, 503, rec.Code, "no eligible channel must stop before the downstream billing/relay handler")
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", second.Id).Update("settings", `{}`).Error)
		})
	}
	require.EqualValues(t, 2, allowedCalls.Load())
}

func TestProbeAutoGroupDoesNotEscape(t *testing.T) {
	requireDB(t)
	oldGroups := setting.UserUsableGroups2JSONString()
	oldRatios := ratio_setting.GroupRatio2JSONString()
	oldMemory := common.MemoryCacheEnabled
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
		common.MemoryCacheEnabled = oldMemory
		model.InitChannelCache()
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	modelName := uniq("probe-auto")
	first := mkChannel(t, func(ch *model.Channel) {
		ch.Group = "vip"
		ch.Models = modelName
		ch.OtherSettings = `{"disable_probe_requests":true}`
	})
	second := mkChannel(t, func(ch *model.Channel) { ch.Group = "default"; ch.Models = modelName })
	for _, ch := range []*model.Channel{first, second} {
		require.NoError(t, ch.AddAbilities(nil))
		t.Cleanup(func() { model.DB.Where("channel_id = ?", ch.Id).Delete(&model.Ability{}) })
	}
	for _, memory := range []bool{false, true} {
		common.MemoryCacheEnabled = memory
		model.InitChannelCache()
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
		common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)
		ctx.Set(service.ProbeRoutingContextKey, &service.RequestProbeRouting{Policy: model.CurrentChannelProbePolicy(), InputChars: 2})
		param := &service.RetryParam{Ctx: ctx, TokenGroup: "auto", ModelName: modelName, RequestPath: "/v1/responses", Retry: common.GetPointer(0)}
		_, group, err := service.CacheGetRandomSatisfiedChannel(param)
		require.ErrorIs(t, err, model.ErrProbeChannelUnavailable)
		require.Equal(t, "vip", group)
		require.Equal(t, "vip", service.GetRequestProbeRouting(ctx).Group)
		param.IncreaseRetry()
		_, group, err = service.CacheGetRandomSatisfiedChannel(param)
		require.ErrorIs(t, err, model.ErrProbeChannelUnavailable)
		require.Equal(t, "vip", group)
		require.Equal(t, 1, param.GetRetry())
	}
}
