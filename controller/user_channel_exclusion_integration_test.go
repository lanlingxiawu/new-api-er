package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type exclusionHTTPFixture struct {
	billing             *responsesWSBillingTest
	low                 *model.Channel
	url                 string
	client              *http.Client
	highCalls, lowCalls atomic.Int64
	requestID           atomic.Int64
}

func newExclusionHTTPFixture(t *testing.T) *exclusionHTTPFixture {
	t.Helper()
	previousErrors := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrors })
	f := &exclusionHTTPFixture{billing: newResponsesWSBillingTest(t, `tier("request", fixed(0.000002))`, func(*websocket.Conn, *http.Request) {})}
	require.NoError(t, model.DB.AutoMigrate(&model.UserChannelExclusion{}, &model.UserChannelExclusionCleanup{}, &model.SystemTaskLock{}, &model.CasbinRule{}, &model.AuthzRole{}))
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelCostConfig{}, &model.ChannelDailyUsage{}, &model.ChannelLimitPeriodUsage{}, &model.PlatformChannelDailyStat{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.AuditLog{}))
	require.NoError(t, authz.Init(model.DB))
	require.NoError(t, model.DB.Model(f.billing.user).Update("quota", 1000000000).Error)
	require.NoError(t, model.DB.Model(f.billing.token).Update("remain_quota", 1000000000).Error)
	for _, target := range []struct {
		label    string
		calls    *atomic.Int64
		priority int64
	}{{"high", &f.highCalls, 100}, {"low", &f.lowCalls, 1}} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target.calls.Add(1)
			var request struct {
				Stream bool `json:"stream"`
			}
			if common.DecodeJson(r.Body, &request) != nil {
				w.WriteHeader(400)
				return
			}
			response := fmt.Sprintf(`{"id":"%s","object":"response","status":"completed","model":"gpt-4o","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, target.label)
			if request.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\ndata: [DONE]\n\n", response)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, response)
		}))
		t.Cleanup(upstream.Close)
		if target.label == "high" {
			require.NoError(t, model.DB.Model(f.billing.channel).Updates(map[string]any{"base_url": upstream.URL, "priority": target.priority}).Error)
			require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", f.billing.channel.Id).Update("priority", target.priority).Error)
		} else {
			f.low = &model.Channel{Name: "exclusion-low", Key: "mock-only", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "default", Models: "ws-billing", BaseURL: &upstream.URL, Priority: common.GetPointer(target.priority)}
			require.NoError(t, model.DB.Create(f.low).Error)
			require.NoError(t, model.DB.Create(&model.Ability{ChannelId: f.low.Id, Group: "default", Model: "ws-billing", Enabled: true, Priority: f.low.Priority}).Error)
			t.Cleanup(func() { model.DB.Where("channel_id = ?", f.low.Id).Delete(&model.Ability{}); model.DB.Delete(f.low) })
		}
	}
	common.MemoryCacheEnabled = true
	model.InitChannelCache()
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		id := fmt.Sprintf("exclusion-%d-%d", f.billing.user.Id, f.requestID.Add(1))
		c.Set(common.RequestIdKey, id)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), common.RequestIdKey, id))
		c.Next()
	})
	engine.POST("/v1/responses", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
	server := httptest.NewServer(engine)
	f.url = server.URL + "/v1/responses"
	f.client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128}}
	t.Cleanup(func() { server.Close(); f.client.CloseIdleConnections(); drainRelayLogsForTest(t) })
	t.Cleanup(func() {
		model.DB.Where("user_id = ?", f.billing.user.Id).Delete(&model.UserChannelExclusion{})
		model.DB.Where("channel_id IN ?", []int{f.billing.channel.Id, f.low.Id}).Delete(&model.UserChannelExclusionCleanup{})
	})
	return f
}

func (f *exclusionHTTPFixture) save(t *testing.T, ids []int) {
	t.Helper()
	c, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{"id": f.billing.user.Id, "username": f.billing.user.Username, "group": "default", "excluded_channel_ids": ids})
	asAdmin(c, nextTestID())
	c.Set("role", common.RoleRootUser)
	UpdateUser(c)
	response := decodeResp(t, rec)
	require.True(t, response.Success, response.Message)
}

func (f *exclusionHTTPFixture) request(stream bool) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost, f.url, strings.NewReader(fmt.Sprintf(`{"model":"ws-billing","input":"hello","max_output_tokens":1,"stream":%t}`, stream)))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer sk-"+f.billing.token.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

func TestUserChannelExclusionHTTPIntegration(t *testing.T) {
	f := newExclusionHTTPFixture(t)
	for _, step := range []struct {
		name     string
		ids      []int
		code     int
		upstream string
	}{
		{"empty", []int{}, 200, "high"},
		{"fallback", []int{f.billing.channel.Id}, 200, "low"},
		{"all excluded", []int{f.billing.channel.Id, f.low.Id}, 503, ""},
		{"clear", []int{}, 200, "high"},
	} {
		t.Run(step.name, func(t *testing.T) {
			f.save(t, step.ids)
			for _, stream := range []bool{false, true} {
				before := f.highCalls.Load() + f.lowCalls.Load()
				code, body, err := f.request(stream)
				require.NoError(t, err)
				require.Equal(t, step.code, code, body)
				if code == 200 {
					require.Contains(t, body, `"id":"`+step.upstream+`"`)
				} else {
					require.NotContains(t, body, "excluded")
					require.NotContains(t, body, "user_channel")
					require.Equal(t, before, f.highCalls.Load()+f.lowCalls.Load())
				}
			}
		})
	}
	// Channel deletion runs the same durable DB/cache protocol as the worker.
	f.save(t, []int{f.billing.channel.Id, f.low.Id})
	require.NoError(t, f.billing.channel.Delete())
	model.InitChannelCache()
	lease := model.SystemTaskLock{Type: model.SystemTaskTypeUserChannelExclusionCleanup, TaskID: "exclusion-http", LockedBy: "test", LockedUntil: time.Now().Add(time.Minute).Unix()}
	require.NoError(t, model.DB.Create(&lease).Error)
	t.Cleanup(func() {
		model.DB.Where("type = ? AND task_id = ?", lease.Type, lease.TaskID).Delete(&model.SystemTaskLock{})
	})
	id, err := model.PrepareUserChannelExclusionCleanup(context.Background(), f.billing.channel.Id, lease.TaskID, lease.LockedBy)
	require.NoError(t, err)
	require.Equal(t, f.billing.user.Id, id)
	require.NoError(t, model.PublishUserChannelExclusionCleanup(context.Background(), id))
	require.NoError(t, model.AcknowledgeUserChannelExclusionCleanup(context.Background(), f.billing.channel.Id, id, lease.TaskID, lease.LockedBy))
	var user model.User
	require.NoError(t, model.DB.First(&user, id).Error)
	require.Equal(t, fmt.Sprintf("[%d]", f.low.Id), user.ExcludedChannelIDs)
	code, body, err := f.request(false)
	require.NoError(t, err)
	require.Equal(t, 503, code, body)
	f.save(t, []int{})
	code, body, err = f.request(false)
	require.NoError(t, err)
	require.Equal(t, 200, code, body)
	require.Contains(t, body, `"id":"low"`)
	drainRelayLogsForTest(t)
	var rejected []model.Log
	require.NoError(t, model.LOG_DB.Where("token_id = ? AND type = ?", f.billing.token.Id, model.LogTypeError).Find(&rejected).Error)
	require.Len(t, rejected, 3)
	for _, entry := range rejected {
		require.Equal(t, "ws-billing", entry.ModelName, "first-selection failures must remain searchable by requested model")
		require.Contains(t, entry.Other, "no_eligible_channel_after_user_exclusion")
		require.Zero(t, entry.Quota)
	}
	require.NoError(t, model.DB.First(&user, id).Error)
	require.Equal(t, 7, user.UsedQuota, "rejected requests must not be charged")
	require.Equal(t, 1000000000-7, user.Quota)
}

// Opt-in staging test: real loopback HTTP, Redis and SQL; never a real AI provider.
// Run alone in a disposable process/database because InitBatchUpdater is process-scoped.
func TestUserChannelExclusionLoad(t *testing.T) {
	if os.Getenv("TEST_EXCLUSION_LOAD") != "1" {
		t.Skip("set TEST_EXCLUSION_LOAD=1 with disposable SQL/Redis fixtures")
	}
	require.Contains(t, os.Getenv("TEST_RESPONSES_SQL_DSN"), "/exclusion_http?")
	require.Contains(t, os.Getenv("TEST_RESPONSES_LOG_SQL_DSN"), "/exclusion_http?")
	responsesWSTestUserSequence.Store(time.Now().UnixNano() % 1000000000)
	f := newExclusionHTTPFixture(t)
	options, err := redis.ParseURL(os.Getenv("TEST_EXCLUSION_REDIS_URL"))
	require.NoError(t, err)
	require.Contains(t, options.Addr, "127.0.0.1:")
	options.DB = 13 // Dedicated disposable test database, never flush another namespace.
	rdb := redis.NewClient(options)
	require.NoError(t, rdb.Ping(context.Background()).Err())
	previous := common.RDB
	common.RDB = rdb
	t.Cleanup(func() { common.RDB = previous; require.NoError(t, rdb.Close()) })
	for _, db := range []*gorm.DB{model.DB, model.LOG_DB} {
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(32)
		pool.SetMaxIdleConns(16)
	}
	common.BatchUpdateEnabled = true
	interval := common.BatchUpdateInterval
	common.BatchUpdateInterval = 1
	model.InitBatchUpdater()
	t.Cleanup(func() { common.BatchUpdateInterval = interval })
	unused := make([]model.Channel, 255)
	maxIDs := []int{f.billing.channel.Id}
	for i := range unused {
		unused[i] = model.Channel{Name: fmt.Sprintf("exclusion-unused-%d", i), Key: "mock-only", Status: common.ChannelStatusManuallyDisabled, Type: constant.ChannelTypeOpenAI}
	}
	require.NoError(t, model.DB.CreateInBatches(&unused, 100).Error)
	for _, ch := range unused {
		maxIDs = append(maxIDs, ch.Id)
	}
	t.Cleanup(func() { model.DB.Where("id IN ?", maxIDs[1:]).Delete(&model.Channel{}) })
	var report []map[string]any
	var totalSuccess int64
	var totalRejected int64
	for _, stage := range []struct {
		name string
		rps  int
		ids  []int
		want string
	}{
		{"baseline_30k", 500, []int{}, "high"},
		{"baseline_100k", 1667, []int{}, "high"},
		{"one_excluded_100k", 1667, []int{f.billing.channel.Id}, "low"},
		{"256_excluded_100k", 1667, maxIDs, "low"},
		{"all_excluded_100k", 1667, []int{f.billing.channel.Id, f.low.Id}, ""},
	} {
		f.save(t, stage.ids)
		const seconds = 10
		n := stage.rps * seconds
		latencies := make([]float64, n)
		var next, failures, success atomic.Int64
		var firstError string
		var errorOnce sync.Once
		highBefore, lowBefore := f.highCalls.Load(), f.lowCalls.Load()
		start := time.Now()
		var wg sync.WaitGroup
		for range 128 {
			wg.Go(func() {
				for {
					i := int(next.Add(1) - 1)
					if i >= n {
						return
					}
					at := start.Add(time.Duration(i) * time.Second / time.Duration(stage.rps))
					time.Sleep(time.Until(at))
					code, body, err := f.request(i%2 == 0)
					latencies[i] = float64(time.Since(at).Microseconds()) / 1000 // includes scheduling delay
					valid := err == nil
					if stage.want == "" {
						valid = valid && code == 503 && !strings.Contains(body, "user_channel") && !strings.Contains(body, "excluded")
					} else {
						valid = valid && code == 200 && strings.Contains(body, `"id":"`+stage.want+`"`)
					}
					if !valid {
						failures.Add(1)
						errorOnce.Do(func() { firstError = fmt.Sprintf("status=%d err=%v body=%.250s", code, err, body) })
					} else {
						success.Add(1)
					}
				}
			})
		}
		wg.Wait()
		elapsed := time.Since(start).Seconds()
		slices.Sort(latencies)
		highDelta, lowDelta := f.highCalls.Load()-highBefore, f.lowCalls.Load()-lowBefore
		row := map[string]any{"stage": stage.name, "target_rps": stage.rps, "requests": n, "validated": success.Load(), "failures": failures.Load(), "actual_rps": float64(n) / elapsed, "p50_ms": latencies[n/2], "p95_ms": latencies[n*95/100], "p99_ms": latencies[n*99/100], "high_calls": highDelta, "low_calls": lowDelta, "first_error": firstError}
		report = append(report, row)
		encoded, _ := common.Marshal(row)
		t.Log(string(encoded))
		require.Zero(t, failures.Load(), firstError)
		if stage.want == "low" {
			require.Zero(t, highDelta)
			require.EqualValues(t, n, lowDelta)
		}
		if stage.want == "high" {
			require.Zero(t, lowDelta)
			require.EqualValues(t, n, highDelta)
		}
		if stage.want == "" {
			require.Zero(t, highDelta+lowDelta)
			totalRejected += success.Load()
		} else {
			totalSuccess += success.Load()
		}
	}
	drainRelayLogsForTest(t)
	require.Eventually(t, func() bool {
		var u model.User
		var token model.Token
		if model.DB.First(&u, f.billing.user.Id).Error != nil || model.DB.First(&token, f.billing.token.Id).Error != nil {
			return false
		}
		return int64(u.UsedQuota) == totalSuccess && int64(token.UsedQuota) == totalSuccess && int64(u.Quota) == 1000000000-totalSuccess && int64(token.RemainQuota) == 1000000000-totalSuccess
	}, 15*time.Second, 100*time.Millisecond, "batch accounting must drain exactly")
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("token_id = ? AND type = ?", f.billing.token.Id, model.LogTypeConsume).Count(&count).Error)
	require.Equal(t, totalSuccess, count)
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("token_id = ? AND type = ? AND quota = 0 AND channel_id = 0 AND model_name = ?", f.billing.token.Id, model.LogTypeError, "ws-billing").Count(&count).Error)
	require.Equal(t, totalRejected, count, "first selection rejections must remain visible in logs under load")
	require.Eventually(t, func() bool {
		user, err := model.GetUserCache(f.billing.user.Id)
		return err == nil && int64(user.Quota) == 1000000000-totalSuccess
	}, 10*time.Second, 100*time.Millisecond, "Redis wallet must match settled SQL accounting")
	if path := os.Getenv("TEST_EXCLUSION_LOAD_REPORT"); path != "" {
		raw, err := common.Marshal(map[string]any{"stages": report, "accounting": map[string]any{"successes": totalSuccess, "expected_rejections": totalRejected, "sql_and_redis_wallet_match": true, "consume_and_error_log_counts_match": true}, "workers": 128, "seconds_per_stage": 10, "stream_ratio": 0.5, "upstream": "loopback mock", "batch_accounting": true})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0600))
	}
}
