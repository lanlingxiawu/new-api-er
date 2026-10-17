package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestTextHelper_FinalOverridesReachUpstreamWithoutUnsafeAdaptation(t *testing.T) {
	cases := []struct {
		name     string
		override map[string]any
		adapt    bool
	}{
		{"ordinary", map[string]any{"temperature": 0}, true},
		{"audio", map[string]any{"audio": map[string]any{"format": "wav"}}, false},
		{"legacy functions", map[string]any{"functions": []any{}}, false},
		{"multiple choices", map[string]any{"n": 2}, false},
		{"logprobs operation", map[string]any{"operations": []any{map[string]any{"mode": "set", "path": "logprobs", "value": true}}}, false},
		{"usage override", map[string]any{"stream_options": map[string]any{"include_usage": false}}, false},
		{"explicit SSE", map[string]any{"stream": true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan []byte, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received <- body
				w.Header().Set("Content-Type", "application/json")
				// A non-retryable response exercises the actual send without
				// touching funding or a database in this request-transform test.
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"fixture","type":"rate_limit_error"}}`))
			}))
			defer upstream.Close()
			c, info, request := adaptCase(t)
			request.Model = "gpt-4o"
			info.Request = request
			info.OriginModelName = "gpt-4o"
			info.RequestURLPath = "/v1/chat/completions"
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, tc.override)
			apiErr := TextHelper(c, info)
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			require.Len(t, received, 1)
			body := <-received
			assert.Equal(t, tc.adapt, info.UpstreamStreamAdapted)
			assert.Nil(t, request.Stream, "the client's request must not be rewritten")
			for key, value := range tc.override {
				if key == "operations" {
					continue
				}
				expected, err := common.Marshal(value)
				require.NoError(t, err)
				assert.JSONEq(t, string(expected), gjson.GetBytes(body, key).Raw, key)
			}
			if tc.adapt {
				assert.True(t, gjson.GetBytes(body, "stream").Bool())
				assert.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
				assert.Equal(t, "0", gjson.GetBytes(body, "temperature").Raw)
			} else if tc.name != "explicit SSE" {
				assert.False(t, gjson.GetBytes(body, "stream").Bool())
			}
			if tc.name == "logprobs operation" {
				assert.True(t, gjson.GetBytes(body, "logprobs").Bool())
			}
		})
	}
}

// The actual outbound body, including channel overrides, determines whether
// aggregation can preserve the response. Rejected bodies must stay untouched.
func TestAdaptFinalUpstreamStreamRequest(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		adapt bool
	}{
		{"plain", `{"model":"m","stream":false,"temperature":0}`, true},
		{"single choice and text", `{"n":1,"modalities":["text"],"logprobs":false}`, true},
		{"null optional fields", `{"n":null,"audio":null,"functions":null,"top_logprobs":null}`, true},
		{"tool calls", `{"tools":[{"type":"function","function":{"name":"f"}}]}`, true},
		{"preserve stream options", `{"stream_options":{"include_usage":true,"include_obfuscation":false}}`, true},
		{"multiple choices", `{"n":2}`, false},
		{"zero choices", `{"n":0}`, false},
		{"invalid choices", `{"n":"1"}`, false},
		{"logprobs", `{"logprobs":true}`, false},
		{"top logprobs zero", `{"top_logprobs":0}`, false},
		{"audio", `{"audio":{"format":"wav"}}`, false},
		{"audio modality", `{"modalities":["text","audio"]}`, false},
		{"invalid modalities", `{"modalities":"text"}`, false},
		{"legacy functions", `{"functions":[]}`, false},
		{"legacy function call", `{"function_call":"none"}`, false},
		{"explicit upstream stream", `{"stream":true}`, false},
		{"invalid stream", `{"stream":"false"}`, false},
		{"usage disabled", `{"stream_options":{"include_usage":false}}`, false},
		{"invalid stream options", `{"stream_options":[]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info, request := adaptCase(t)
			body, err := adaptFinalUpstreamStreamRequest(c, info, request, []byte(tc.body))
			require.NoError(t, err)
			assert.Equal(t, tc.adapt, info.UpstreamStreamAdapted)
			assert.False(t, info.IsStream)
			if !tc.adapt {
				assert.Equal(t, tc.body, string(body), "do not rewrite channel overrides")
				return
			}
			assert.True(t, gjson.GetBytes(body, "stream").Bool())
			assert.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
			for _, key := range []string{"model", "temperature", "n", "modalities", "logprobs", "tools", "stream_options.include_obfuscation"} {
				assert.Equal(t, gjson.Get(tc.body, key).Raw, gjson.GetBytes(body, key).Raw, key)
			}
		})
	}
	for _, mode := range []string{"refund", "input"} {
		t.Run(mode, func(t *testing.T) {
			c, info, request := adaptCase(t)
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, mode)
			body, err := adaptFinalUpstreamStreamRequest(c, info, request, []byte(`{"stream":false}`))
			require.NoError(t, err)
			assert.JSONEq(t, `{"stream":false}`, string(body))
			assert.False(t, info.UpstreamStreamAdapted)
		})
	}
}

func TestAdaptFinalUpstreamStreamRequest_PlainFallbackRestoresOriginalFields(t *testing.T) {
	for _, original := range []string{
		`{"model":"m","temperature":0}`,
		`{"model":"m","stream":false,"stream_options":{"include_usage":true,"include_obfuscation":false}}`,
		`{"model":"m","stream":null,"stream_options":null}`,
	} {
		t.Run(original, func(t *testing.T) {
			c, info, request := adaptCase(t)
			body, err := adaptFinalUpstreamStreamRequest(c, info, request, []byte(original))
			require.NoError(t, err)
			require.True(t, info.UpstreamStreamAdapted)
			storage, err := common.CreateBodyStorage(body)
			require.NoError(t, err)
			defer storage.Close()
			adaptor := &recordingAdaptor{status: http.StatusTooManyRequests}
			resp, closer, apiErr := fallBackFromRejectedAdaptation(c, info, adaptor, rejected(http.StatusBadRequest), storage)
			if closer != nil {
				defer closer.Close()
			}
			require.Nil(t, apiErr)
			require.NotNil(t, resp)
			defer resp.Body.Close()
			require.Len(t, adaptor.sent, 1)
			assert.JSONEq(t, original, string(adaptor.sent[0]))
			assert.False(t, info.UpstreamStreamAdapted)
		})
	}
}

// adaptCase builds the fully-eligible baseline; each test mutates exactly one
// input so a failure names the single condition that regressed.
func adaptCase(t *testing.T) (*gin.Context, *relaycommon.RelayInfo, *dto.GeneralOpenAIRequest) {
	if t != nil {
		t.Helper()
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, "charge")
	// A timeout control with a pending deadline: the request can time out.
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, deadlineControl{deadline: time.Now().Add(time.Minute)})

	info := &relaycommon.RelayInfo{
		IsStream:    false,
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{SupportStreamOptions: true, ApiType: constant.APITypeOpenAI},
	}

	request := &dto.GeneralOpenAIRequest{Model: "gpt-4o"}
	return c, info, request
}

// deadlineControl stands in for the timeout controller as seen through
// service.RelayRequestDeadline; a zero deadline means no limit is pending.
type deadlineControl struct {
	deadline time.Time
}

func (deadlineControl) MarkResponse(bool) bool { return false }

func (d deadlineControl) RelayTimeoutDeadline() (time.Time, bool) {
	return d.deadline, !d.deadline.IsZero()
}

func TestShouldAdaptUpstreamStream_BaselineEligible(t *testing.T) {
	c, info, request := adaptCase(t)
	assert.True(t, shouldAdaptUpstreamStream(c, info, request))
}

func TestShouldAdaptUpstreamStream_EachConditionRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest)
	}{
		{"client asked for stream", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			info.IsStream = true
		}},
		{"billing mode is refund", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, "refund")
		}},
		{"billing mode unset", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			c.Set(string(constant.ContextKeyUserNonStreamTimeoutBilling), "")
		}},
		{"request not owned by timeout subsystem", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			delete(c.Keys, string(constant.ContextKeyRelayTimeoutControl))
		}},
		{"owned by timeout subsystem but no deadline pending", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, deadlineControl{})
		}},
		{"timeout control that cannot report a deadline", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, struct{}{})
		}},
		{"non-openai relay format", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			info.RelayFormat = types.RelayFormatClaude
		}},
		{"not chat completions", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			info.RelayMode = relayconstant.RelayModeEmbeddings
		}},
		{"channel outside stream whitelist", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			info.SupportStreamOptions = false
		}},
		{"n greater than one", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			r.N = common.GetPointer(2)
		}},
		{"logprobs requested", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			r.LogProbs = common.GetPointer(true)
		}},
		{"top_logprobs requested", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			r.TopLogProbs = common.GetPointer(3)
		}},
		{"channel body pass-through", func(c *gin.Context, info *relaycommon.RelayInfo, r *dto.GeneralOpenAIRequest) {
			info.ChannelSetting.PassThroughBodyEnabled = true
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info, request := adaptCase(t)
			tc.mutate(c, info, request)
			assert.False(t, shouldAdaptUpstreamStream(c, info, request))
		})
	}
}

func TestShouldAdaptUpstreamStream_GlobalPassThroughRejects(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	prev := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = true
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = prev })

	c, info, request := adaptCase(t)
	assert.False(t, shouldAdaptUpstreamStream(c, info, request))
}

// n is optional; absent and an explicit 1 are both ordinary single-choice
// requests and must stay eligible.
func TestShouldAdaptUpstreamStream_NBoundary(t *testing.T) {
	cases := []struct {
		name string
		n    *int
		want bool
	}{
		{"absent", nil, true},
		{"explicit 1", common.GetPointer(1), true},
		{"2", common.GetPointer(2), false},
		{"0", common.GetPointer(0), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info, request := adaptCase(t)
			request.N = tc.n
			assert.Equal(t, tc.want, shouldAdaptUpstreamStream(c, info, request))
		})
	}
}

// logprobs:false is an explicit opt-out, not a request for logprobs.
func TestShouldAdaptUpstreamStream_LogProbsFalseStaysEligible(t *testing.T) {
	c, info, request := adaptCase(t)
	request.LogProbs = common.GetPointer(false)
	assert.True(t, shouldAdaptUpstreamStream(c, info, request))
}

func TestShouldAdaptUpstreamStream_NilInputs(t *testing.T) {
	c, info, request := adaptCase(t)
	assert.False(t, shouldAdaptUpstreamStream(nil, info, request))
	assert.False(t, shouldAdaptUpstreamStream(c, nil, request))
	assert.False(t, shouldAdaptUpstreamStream(c, info, nil))
}

func TestApplyUpstreamStreamAdaptation(t *testing.T) {
	c, info, request := adaptCase(t)
	applyUpstreamStreamAdaptation(c, info, request)

	require.NotNil(t, request.Stream)
	assert.True(t, *request.Stream)
	require.NotNil(t, request.StreamOptions)
	assert.True(t, request.StreamOptions.IncludeUsage,
		"without include_usage every settlement falls back to estimation")
	assert.True(t, info.UpstreamStreamAdapted)
	assert.True(t, info.ShouldIncludeUsage)
	assert.False(t, info.IsStream,
		"the client-facing mode must stay non-stream")
}

func TestApplyUpstreamStreamAdaptation_NilSafe(t *testing.T) {
	c, info, request := adaptCase(t)
	assert.NotPanics(t, func() { applyUpstreamStreamAdaptation(c, nil, request) })
	assert.NotPanics(t, func() { applyUpstreamStreamAdaptation(c, info, nil) })
	assert.False(t, info.UpstreamStreamAdapted)
}

// relayInfo is reused across the controller's retry loop, so adaptation state
// from a previous attempt must never decide the current one. The reset lives in
// controller/relay.go next to ReceivedResponseCount, for the same reason.
func TestUpstreamStreamAdaptation_DoesNotLeakAcrossAttempts(t *testing.T) {
	c, info, request := adaptCase(t)

	// Attempt 1: channel supports streaming, so the request is adapted.
	require.True(t, shouldAdaptUpstreamStream(c, info, request))
	applyUpstreamStreamAdaptation(c, info, request)
	require.True(t, info.UpstreamStreamAdapted)
	require.NotNil(t, request.Stream)

	// Retry: the controller clears the flag before the next attempt, and
	// TextHelper works on a fresh DeepCopy of the client request.
	info.UpstreamStreamAdapted = false
	info.SupportStreamOptions = false
	retryRequest := &dto.GeneralOpenAIRequest{Model: "gpt-4o"}

	assert.False(t, shouldAdaptUpstreamStream(c, info, retryRequest),
		"a retry onto a non-streaming channel must not adapt")
	assert.False(t, info.UpstreamStreamAdapted,
		"the previous attempt's adaptation must not survive into this one")
	assert.Nil(t, retryRequest.Stream,
		"the retry request must not inherit the rewritten stream flag")
}

// RelayFormat is the client's protocol and says nothing about the upstream's.
// streamSupportedChannels contains Anthropic/Gemini/AWS, whose native SSE is not
// chat.completion.chunk — adapting those would hand the buffered handler frames
// it parses into empty chunks, silently dropping the model's output while still
// billing. Only an OpenAI-wire upstream may be adapted.
func TestShouldAdaptUpstreamStream_OnlyOpenAIWireUpstream(t *testing.T) {
	cases := []struct {
		name    string
		apiType int
		want    bool
	}{
		{"openai upstream", constant.APITypeOpenAI, true},
		{"anthropic upstream", constant.APITypeAnthropic, false},
		{"gemini upstream", constant.APITypeGemini, false},
		{"aws upstream", constant.APITypeAws, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info, request := adaptCase(t)
			info.ApiType = tc.apiType
			assert.Equal(t, tc.want, shouldAdaptUpstreamStream(c, info, request))
		})
	}
}
