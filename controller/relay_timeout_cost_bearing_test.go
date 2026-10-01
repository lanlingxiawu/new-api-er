package controller

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end tests for docs/design/relay-timeout-cost-bearing.md §14, through the
// real middleware chain (TokenAuth → RelayRequestTimeout → Distribute) and the
// relay controllers, against httptest upstreams (Rule 15.4) and the real DB
// (balances, consume/error logs). The user's own timeout is 1s; hanging
// upstreams never answer within it, slow ones answer after it.

const costBearingRatio = 2.0 // model ratio of the token-priced test model

// costBearingFixture is one user/token/channel set against a scripted upstream.
type costBearingFixture struct {
	user      *model.User
	token     *model.Token
	modelName string
	quota     int
	engine    *gin.Engine
	hits      atomic.Int32
}

type costBearingOptions struct {
	channelType int
	perCall     float64 // > 0 prices the model per call
	models      string  // defaults to the generated model name
	upstream    http.Handler
}

// newCostBearingFixture builds the fixture and restores every global it touches.
func newCostBearingFixture(t *testing.T, opts costBearingOptions) *costBearingFixture {
	t.Helper()
	requireDB(t)
	f := &costBearingFixture{
		modelName: "cb-" + strings.ReplaceAll(uniq("m"), "_", "-"),
		quota:     1_000_000, // below the trust quota, so pre-consume really happens
	}
	if opts.models == "" {
		opts.models = f.modelName
	}
	prevPrice := ratio_setting.ModelPrice2JSONString()
	prevRatio := ratio_setting.ModelRatio2JSONString()
	prevCompletion := ratio_setting.CompletionRatio2JSONString()
	prevRetry := common.RetryTimes
	prevMemCache := common.MemoryCacheEnabled
	prevErrLog := constant.ErrorLogEnabled
	prevLogConsume := common.LogConsumeEnabled
	prevTimeout := operation_setting.GetRelayTimeoutSetting()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(prevPrice)
		_ = ratio_setting.UpdateModelRatioByJSONString(prevRatio)
		_ = ratio_setting.UpdateCompletionRatioByJSONString(prevCompletion)
		common.RetryTimes = prevRetry
		common.MemoryCacheEnabled = prevMemCache
		constant.ErrorLogEnabled = prevErrLog
		common.LogConsumeEnabled = prevLogConsume
		operation_setting.ReplaceRelayTimeoutSetting(prevTimeout)
	})
	// The production default: the input estimate is what a timed-out request is
	// settled on. With counting disabled the estimate is 0.
	prevCountToken := constant.CountToken
	t.Cleanup(func() { constant.CountToken = prevCountToken })
	constant.CountToken = true
	common.RetryTimes = 0
	common.MemoryCacheEnabled = false
	constant.ErrorLogEnabled = true
	common.LogConsumeEnabled = true
	// Relay logs are written by the async pipeline; start it (once per process)
	// so drains persist them. Flushing happens only on DrainRelayLogsSync.
	operation_setting.GetRelayLogPipelineSetting().FlushIntervalMs = 60_000
	model.StartRelayLogFlushLoop()
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	timeout := prevTimeout
	timeout.Enabled = true
	timeout.MaxTotalAttempts = 0
	timeout.RetryMinBudgetSeconds = 0
	operation_setting.ReplaceRelayTimeoutSetting(timeout)

	setPrice := func(name string) {
		prices := map[string]float64{}
		require.NoError(t, common.UnmarshalJsonStr(prevPrice, &prices))
		prices[name] = opts.perCall
		encoded, err := common.Marshal(prices)
		require.NoError(t, err)
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(encoded)))
	}
	if opts.perCall > 0 {
		for _, name := range strings.Split(opts.models, ",") {
			setPrice(name)
		}
	} else {
		ratios := map[string]float64{}
		require.NoError(t, common.UnmarshalJsonStr(prevRatio, &ratios))
		ratios[f.modelName] = costBearingRatio
		encoded, err := common.Marshal(ratios)
		require.NoError(t, err)
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))
		completions := map[string]float64{}
		require.NoError(t, common.UnmarshalJsonStr(prevCompletion, &completions))
		completions[f.modelName] = 1
		encoded, err = common.Marshal(completions)
		require.NoError(t, err)
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(string(encoded)))
	}

	f.user = mkUser(t, func(u *model.User) { u.Quota = f.quota })
	f.token = mkToken(t, f.user.Id, func(tk *model.Token) { tk.RemainQuota = f.quota })
	t.Cleanup(func() {
		model.DrainRelayLogsSync(5 * time.Second)
		model.LOG_DB.Where("user_id = ?", f.user.Id).Delete(&model.Log{})
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		opts.upstream.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	baseURL := server.URL
	autoBan := 0
	priority := int64(0)
	weight := uint(0)
	channelType := opts.channelType
	if channelType == 0 {
		channelType = constant.ChannelTypeOpenAI
	}
	channel := mkChannel(t, func(ch *model.Channel) {
		ch.Type = channelType
		ch.Key = "sk-upstream-test"
		ch.BaseURL = &baseURL
		ch.Models = opts.models
		ch.Group = "default"
		ch.AutoBan = &autoBan
		ch.Priority = &priority
		ch.Weight = &weight
	})
	require.NoError(t, channel.AddAbilities(nil))
	t.Cleanup(func() { model.DB.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}) })

	f.engine = gin.New()
	f.engine.Use(middleware.TokenAuth(), middleware.RelayRequestTimeout(), middleware.Distribute())
	f.engine.POST("/v1/chat/completions", func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
	f.engine.POST("/suno/submit/:action", RelayTask)
	f.engine.POST("/mj/submit/imagine", RelayMidjourney)
	return f
}

func (f *costBearingFixture) setUser(t *testing.T, columns map[string]any) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.user.Id).Updates(columns).Error)
}

func (f *costBearingFixture) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+f.token.Key)
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	return rec
}

func (f *costBearingFixture) chat(t *testing.T, extra string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, "/v1/chat/completions", `{"model":"`+f.modelName+`",`+extra+`"messages":[{"role":"user","content":"hello there, how are you today?"}]}`)
}

// spent waits for async refunds/settlements and returns how much the user paid.
func (f *costBearingFixture) spent(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		userQuota, tokenQuota := retryAuditStoredQuotas(t, f.user.Id, f.token.Id)
		if f.quota-userQuota == want && f.quota-tokenQuota == want {
			return
		}
		if time.Now().After(deadline) {
			require.Failf(t, "balances did not settle", "user spent %d, token spent %d, want %d", f.quota-userQuota, f.quota-tokenQuota, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *costBearingFixture) logs(t *testing.T, logType int) []model.Log {
	t.Helper()
	model.DrainRelayLogsSync(5 * time.Second)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", f.user.Id, logType).Order("id").Find(&logs).Error)
	return logs
}

// timeoutAbsorbedOf returns other.admin_info.timeout_absorbed, nil when absent.
func timeoutAbsorbedOf(t *testing.T, other string) map[string]any {
	t.Helper()
	m, err := common.StrToMap(other)
	require.NoError(t, err)
	adminInfo, _ := m["admin_info"].(map[string]any)
	absorbed, _ := adminInfo["timeout_absorbed"].(map[string]any)
	return absorbed
}

// hangUpstream never answers within the user's 1s window.
func hangUpstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
}

// sseThenHang opens a stream (200 + headers), optionally sends frames, then stalls.
func sseThenHang(frames ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, frame := range frames {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
}

// TestTimeoutCostNonStreamMatrix 非流式、未转流、我方超时：refund 全额退款并在错误日志记平台承担的
// 输入费用；input 与 charge（无法转流）按估算输入结算、输出 0；按次计费在 input 档退款并记整次价格；
// 上游错误（非我方超时）在 input 档照旧退款。
func TestTimeoutCostNonStreamMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		perCall    bool
		extra      string // extra request fields; n=2 cannot be stream-adapted
		upstream   http.Handler
		settles    bool
	}{
		{name: "refund", mode: "refund", upstream: hangUpstream()},
		{name: "input", mode: "input", upstream: hangUpstream(), settles: true},
		{name: "charge unadapted", mode: "charge", extra: `"n":2,`, upstream: hangUpstream(), settles: true},
		{name: "input per call", mode: "input", perCall: true, upstream: hangUpstream()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := costBearingOptions{upstream: tc.upstream}
			if tc.perCall {
				opts.perCall = 0.01
			}
			f := newCostBearingFixture(t, opts)
			f.setUser(t, map[string]any{"non_stream_response_timeout": 1, "non_stream_timeout_billing": tc.mode})

			started := time.Now()
			rec := f.chat(t, tc.extra)
			require.Equal(t, http.StatusGatewayTimeout, rec.Code, rec.Body.String())
			assert.Less(t, time.Since(started), 4*time.Second)

			consumes := f.logs(t, model.LogTypeConsume)
			errs := f.logs(t, model.LogTypeError)
			require.Len(t, errs, 1, "the timed-out channel is still reported")
			if !tc.settles {
				f.spent(t, 0)
				assert.Empty(t, consumes)
				absorbed := timeoutAbsorbedOf(t, errs[0].Other)
				require.NotNil(t, absorbed, errs[0].Other)
				assert.Equal(t, tc.mode, absorbed["mode"])
				assert.Equal(t, "non_stream", absorbed["kind"])
				assert.Equal(t, true, absorbed["input_estimated"])
				input := int(absorbed["input_tokens"].(float64))
				require.Positive(t, input)
				want := int(float64(input) * costBearingRatio)
				if tc.perCall {
					want = int(0.01 * common.QuotaPerUnit)
					assert.Equal(t, true, absorbed["per_call"])
				}
				assert.EqualValues(t, want, absorbed["absorbed_quota_min"])
				return
			}
			require.Len(t, consumes, 1)
			log := consumes[0]
			require.Positive(t, log.PromptTokens)
			assert.Zero(t, log.CompletionTokens)
			assert.Equal(t, int(float64(log.PromptTokens)*costBearingRatio), log.Quota)
			assert.Contains(t, log.Content, "charged for input only")
			f.spent(t, log.Quota)
			absorbed := timeoutAbsorbedOf(t, log.Other)
			require.NotNil(t, absorbed, log.Other)
			assert.Equal(t, tc.mode, absorbed["mode"])
			assert.EqualValues(t, 0, absorbed["absorbed_quota_min"])
			assert.Nil(t, timeoutAbsorbedOf(t, errs[0].Other), "recorded once per request")
		})
	}
}

// TestTimeoutCostUpstreamErrorStillRefunds 只有我方超时触发 input 结算：上游 500 照旧退款。
func TestTimeoutCostUpstreamErrorStillRefunds(t *testing.T) {
	f := newCostBearingFixture(t, costBearingOptions{upstream: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream boom","type":"server_error","code":"boom"}}`)
	})})
	f.setUser(t, map[string]any{"non_stream_response_timeout": 1, "non_stream_timeout_billing": "input"})
	rec := f.chat(t, "")
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	f.spent(t, 0)
	assert.Empty(t, f.logs(t, model.LogTypeConsume))
	for _, log := range f.logs(t, model.LogTypeError) {
		assert.Nil(t, timeoutAbsorbedOf(t, log.Other))
	}
}

const costBearingContentChunk = `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"partial answer that was delivered"}}]}`

// TestTimeoutCostStreamMatrix 流式、我方超时：无有效交付时 refund 不收费并在错误日志记录，
// input 按估算输入、输出 0 结算，charge 不变；已有交付时各档位按交付结算且不记录。
func TestTimeoutCostStreamMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		frames     []string
		charged    bool
		record     bool
		output     bool
	}{
		{name: "refund no delivery", mode: "refund", record: true},
		{name: "input no delivery", mode: "input", charged: true, record: true},
		{name: "charge no delivery", mode: "charge", charged: true},
		{name: "input with delivery", mode: "input", frames: []string{costBearingContentChunk}, charged: true, output: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCostBearingFixture(t, costBearingOptions{upstream: sseThenHang(tc.frames...)})
			f.setUser(t, map[string]any{"stream_response_timeout": 1, "stream_total_timeout": 1, "non_stream_timeout_billing": tc.mode})

			rec := f.chat(t, `"stream":true,`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "relay_timeout")

			consumes := f.logs(t, model.LogTypeConsume)
			if !tc.charged {
				f.spent(t, 0)
				assert.Empty(t, consumes)
				errs := f.logs(t, model.LogTypeError)
				require.NotEmpty(t, errs)
				absorbed := timeoutAbsorbedOf(t, errs[len(errs)-1].Other)
				require.NotNil(t, absorbed)
				assert.Equal(t, "refund", absorbed["mode"])
				assert.Equal(t, "stream", absorbed["kind"])
				input := absorbed["input_tokens"].(float64)
				assert.EqualValues(t, int(input*costBearingRatio), absorbed["absorbed_quota_min"])
				return
			}
			require.Len(t, consumes, 1)
			log := consumes[0]
			require.Positive(t, log.PromptTokens)
			if tc.output {
				assert.Positive(t, log.CompletionTokens, "delivered output is billed as before")
			} else {
				assert.Zero(t, log.CompletionTokens)
			}
			f.spent(t, log.Quota)
			absorbed := timeoutAbsorbedOf(t, log.Other)
			if !tc.record {
				assert.Nil(t, absorbed)
				return
			}
			require.NotNil(t, absorbed, log.Other)
			assert.Equal(t, "input", absorbed["mode"])
			assert.Contains(t, log.Content, "charged for input only")
		})
	}
}

// cozeSlowUpstream completes the chat on the second status poll (after ≥1s of
// polling), i.e. after the user's 1s timeout would have fired.
func cozeSlowUpstream() http.Handler {
	var retrieves atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/chat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"data":{"id":"chat1","conversation_id":"conv1","status":"in_progress"}}`)
	})
	mux.HandleFunc("/v3/chat/retrieve", func(w http.ResponseWriter, _ *http.Request) {
		if retrieves.Add(1) < 2 {
			_, _ = io.WriteString(w, `{"code":0,"data":{"status":"in_progress"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{"status":"completed","usage":{"token_count":10,"output_count":6,"input_count":4}}}`)
	})
	mux.HandleFunc("/v3/chat/message/list", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"data":[{"type":"answer","content":"hi"}]}`)
	})
	return mux
}

// slowJSON answers body after delay, as an upstream that accepts a billed job.
func slowJSON(delay time.Duration, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
}

// TestBilledOnSubmissionIgnoresUserTimeout 第 1 项：提交即计费的请求不受单用户超时，
// 慢上游在 1 秒时限之后照常完成并按实际结果计费（与主分支一致）。
func TestBilledOnSubmissionIgnoresUserTimeout(t *testing.T) {
	userTimeouts := map[string]any{
		"non_stream_response_timeout": 1, "non_stream_total_timeout": 1,
		"stream_response_timeout": 1, "stream_total_timeout": 1,
	}
	t.Run("coze non-stream", func(t *testing.T) {
		f := newCostBearingFixture(t, costBearingOptions{channelType: constant.ChannelTypeCoze, upstream: cozeSlowUpstream()})
		f.setUser(t, userTimeouts)
		started := time.Now()
		// Coze needs an explicit user: its default (a raw response id) is not valid JSON (pre-existing, also on main).
		rec := f.chat(t, `"user":"tester",`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Greater(t, time.Since(started), time.Second, "the poll really outlasted the user's timeout")
		consumes := f.logs(t, model.LogTypeConsume)
		require.Len(t, consumes, 1)
		assert.Positive(t, consumes[0].Quota)
		f.spent(t, consumes[0].Quota)
	})
	t.Run("suno task submit", func(t *testing.T) {
		f := newCostBearingFixture(t, costBearingOptions{
			channelType: constant.ChannelTypeSunoAPI, perCall: 0.01, models: "suno_music",
			upstream: slowJSON(1500*time.Millisecond, `{"code":"success","message":"","data":"upstream-task-1"}`),
		})
		f.setUser(t, userTimeouts)
		rec := f.post(t, "/suno/submit/music", `{"prompt":"a song","mv":"chirp-v3-5"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"success"`)
		f.spent(t, int(0.01*common.QuotaPerUnit))
	})
	t.Run("midjourney submit", func(t *testing.T) {
		f := newCostBearingFixture(t, costBearingOptions{
			channelType: constant.ChannelTypeMidjourney, perCall: 0.01, models: "mj_imagine",
			upstream: slowJSON(1500*time.Millisecond, `{"code":1,"description":"submitted","result":"mj-task-1"}`),
		})
		f.setUser(t, userTimeouts)
		rec := f.post(t, "/mj/submit/imagine", `{"prompt":"a cat"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), `"code":4`)
		assert.Contains(t, rec.Body.String(), `"code":1`)
		f.spent(t, int(0.01*common.QuotaPerUnit))
	})
}

// TestRelayBilledOnSubmission 判定表：Coze 非流式与阿里图片生成、图片编辑豁免，其余（含 Coze 流式、阿里对话、
// OpenAI）照常受超时托管。
func TestRelayBilledOnSubmission(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelType int
		stream      bool
		mode        int
		want        bool
	}{
		{"coze non-stream", constant.ChannelTypeCoze, false, relayconstant.RelayModeChatCompletions, true},
		{"coze stream", constant.ChannelTypeCoze, true, relayconstant.RelayModeChatCompletions, false},
		{"ali image generation", constant.ChannelTypeAli, false, relayconstant.RelayModeImagesGenerations, true},
		{"ali image edit (Wan submit and poll)", constant.ChannelTypeAli, false, relayconstant.RelayModeImagesEdits, true},
		{"ali chat", constant.ChannelTypeAli, false, relayconstant.RelayModeChatCompletions, false},
		{"ali chat stream", constant.ChannelTypeAli, true, relayconstant.RelayModeChatCompletions, false},
		{"openai image generation", constant.ChannelTypeOpenAI, false, relayconstant.RelayModeImagesGenerations, false},
		{"openai chat", constant.ChannelTypeOpenAI, false, relayconstant.RelayModeChatCompletions, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channelType)
			info := &relaycommon.RelayInfo{IsStream: tc.stream, RelayMode: tc.mode}
			assert.Equal(t, tc.want, relayBilledOnSubmission(c, info))
		})
	}
}

// TestUserUpdateAcceptsInputTimeoutBilling 管理员更新接口接受 input、拒绝未知值（原值不变）。
func TestUserUpdateAcceptsInputTimeoutBilling(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(u *model.User) { u.NonStreamTimeoutBilling = "charge" })
	for _, tc := range []struct {
		value string
		ok    bool
		want  string
	}{
		{"input", true, "input"},
		{" Input ", true, "input"},
		{"prepaid", false, "input"},
		{"refund", true, "refund"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
				"id": user.Id, "username": user.Username, "group": user.Group,
				"non_stream_timeout_billing": tc.value,
			})
			asAdmin(ctx, nextTestID())
			UpdateUser(ctx)
			assert.Equal(t, tc.ok, decodeResp(t, rec).Success)
			var stored model.User
			require.NoError(t, model.DB.Select("non_stream_timeout_billing").First(&stored, user.Id).Error)
			assert.Equal(t, tc.want, stored.NonStreamTimeoutBilling)
		})
	}
}
