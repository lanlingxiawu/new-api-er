package common

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	commonpkg "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// validateMultipartTaskRequest
// ---------------------------------------------------------------------------

func TestValidateMultipartTaskRequest(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("prompt", "a cat"))
	require.NoError(t, w.WriteField("model", "sora-2"))
	require.NoError(t, w.WriteField("mode", "fast"))
	require.NoError(t, w.WriteField("size", "720x1280"))
	require.NoError(t, w.WriteField("seconds", "8"))
	require.NoError(t, w.WriteField("images", "img1"))
	require.NoError(t, w.WriteField("images", "img2"))
	require.NoError(t, w.WriteField("steps", "20"))    // unknown int -> metadata
	require.NoError(t, w.WriteField("scale", "1.5"))   // unknown float -> metadata
	require.NoError(t, w.WriteField("label", "hello")) // unknown string -> metadata
	require.NoError(t, w.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", &body)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())

	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	req, err := validateMultipartTaskRequest(c, info, constant.TaskActionGenerate)
	require.NoError(t, err)
	assert.Equal(t, "a cat", req.Prompt)
	assert.Equal(t, "sora-2", req.Model)
	assert.Equal(t, 8, req.Duration)
	assert.Equal(t, []string{"img1", "img2"}, req.Images)
	assert.EqualValues(t, 20, req.Metadata["steps"])
	assert.EqualValues(t, 1.5, req.Metadata["scale"])
	assert.Equal(t, "hello", req.Metadata["label"])
}

// ---------------------------------------------------------------------------
// cloneRequestHeaders via genBaseRelayInfo
// ---------------------------------------------------------------------------

func TestCloneRequestHeaders(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Real", "value")
	req.Header.Set("X-Blank", "   ") // trimmed to empty -> skipped
	c.Request = req

	info := GenRelayInfoOpenAI(c, nil)
	require.NotNil(t, info.RequestHeaders)
	assert.Equal(t, "value", info.RequestHeaders["X-Real"])
	_, hasBlank := info.RequestHeaders["X-Blank"]
	assert.False(t, hasBlank)

	// nil context / request path
	assert.Nil(t, cloneRequestHeaders(nil))
}

// ---------------------------------------------------------------------------
// parseHeaderReplacementTokens variants (via set_header value shapes)
// ---------------------------------------------------------------------------

func TestSetHeaderMap_ReplacementArrayTokens(t *testing.T) {
	ctx := map[string]interface{}{
		paramOverrideContextRequestHeaders: map[string]interface{}{},
		paramOverrideContextHeaderOverride: map[string]interface{}{"anthropic-beta": "a"},
	}
	// map a -> ["x","y"] (array replacement tokens)
	_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
		"mode": "set_header", "path": "anthropic-beta",
		"value": map[string]interface{}{"a": []interface{}{"x", "y"}},
	})), ctx)
	require.NoError(t, err)
	ho := ctx[paramOverrideContextHeaderOverride].(map[string]interface{})
	assert.Equal(t, "x,y", ho["anthropic-beta"])
}

func TestSetHeaderMap_ReplacementMapErrors(t *testing.T) {
	ctx := map[string]interface{}{
		paramOverrideContextRequestHeaders: map[string]interface{}{},
		paramOverrideContextHeaderOverride: map[string]interface{}{"anthropic-beta": "a"},
	}
	// map a -> {nested object} is invalid replacement token type
	_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
		"mode": "set_header", "path": "anthropic-beta",
		"value": map[string]interface{}{"a": map[string]interface{}{"bad": true}},
	})), ctx)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// buildParamOverrideAuditLine broad coverage (debug mode, many modes)
// ---------------------------------------------------------------------------

func TestApplyParamOverrideWithRelayInfo_DebugAuditStringAndHeaderModes(t *testing.T) {
	old := commonpkg.DebugEnabled
	commonpkg.DebugEnabled = true
	t.Cleanup(func() { commonpkg.DebugEnabled = old })

	info := &RelayInfo{
		OriginModelName: "gpt-4o",
		RequestHeaders:  map[string]string{"X-Src": "v"},
		ChannelMeta: &ChannelMeta{
			UpstreamModelName: "gpt-4o",
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					op(map[string]interface{}{"path": "model", "mode": "trim_prefix", "value": "openai/"}),
					op(map[string]interface{}{"path": "model", "mode": "ensure_suffix", "value": "-x"}),
					op(map[string]interface{}{"path": "model", "mode": "trim_space"}),
					op(map[string]interface{}{"path": "model", "mode": "replace", "from": "a", "to": "b"}),
					op(map[string]interface{}{"mode": "move", "from": "unused1", "to": "unused2", "conditions": []interface{}{
						map[string]interface{}{"path": "nope", "mode": "full", "value": "x"},
					}}),
					op(map[string]interface{}{"mode": "set_header", "path": "X-H", "value": "hv"}),
					op(map[string]interface{}{"mode": "delete_header", "path": "X-H"}),
					op(map[string]interface{}{"mode": "move_header", "from": "X-Src", "to": "X-Moved"}),
					op(map[string]interface{}{"mode": "pass_headers", "value": []interface{}{"X-Moved"}}),
				},
			},
		},
	}
	_, err := ApplyParamOverrideWithRelayInfo([]byte(`{"model":"openai/gpt"}`), info)
	require.NoError(t, err)
	joined := ""
	for _, l := range info.ParamOverrideAudit {
		joined += l + "\n"
	}
	assert.Contains(t, joined, "trim_prefix model")
	assert.Contains(t, joined, "replace model")
	assert.Contains(t, joined, "set_header")
	assert.Contains(t, joined, "delete_header")
}

// ---------------------------------------------------------------------------
// collectWildcardPaths on array-of-arrays
// ---------------------------------------------------------------------------

func TestApplyParamOverride_WildcardArrayOfArrays(t *testing.T) {
	input := `{"rows":[[{"v":1},{"v":2}],[{"v":3}]]}`
	out, err := apply(t, input, ops(op(map[string]interface{}{
		"path": "rows.*.*.v", "mode": "set", "value": 0,
	})), nil)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `"v":1`)
	assert.Contains(t, string(out), `"v":0`)
}

// ---------------------------------------------------------------------------
// ToString extended branches (audio/reasoning/price/responses tools)
// ---------------------------------------------------------------------------

func TestRelayInfoToString_ExtendedBranches(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:       "openai",
		OriginModelName:   "gpt-4o",
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
		AudioUsage:        true,
		ReasoningEffort:   "high",
		PriceData:         priceDataWithUsePrice(),
		ResponsesUsageInfo: &ResponsesUsageInfo{
			BuiltInTools: map[string]*BuildInToolInfo{
				"web_search_preview": {ToolName: "web_search_preview", CallCount: 2},
				"nil_tool":           nil,
			},
		},
	}
	s := info.ToString()
	assert.Contains(t, s, "Realtime")
	assert.Contains(t, s, "ReasoningEffort")
	assert.Contains(t, s, "PriceData")
	assert.Contains(t, s, "ResponsesTools")
}

// ---------------------------------------------------------------------------
// parsePruneObjectsOptions variants
// ---------------------------------------------------------------------------

func TestPruneObjects_OptionVariants(t *testing.T) {
	t.Run("conditions array with logic OR", func(t *testing.T) {
		input := `{"items":[{"type":"a"},{"type":"b"},{"type":"c"}]}`
		out, err := apply(t, input, ops(op(map[string]interface{}{
			"path": "items", "mode": "prune_objects",
			"value": map[string]interface{}{
				"logic": "OR",
				"conditions": []interface{}{
					map[string]interface{}{"path": "type", "mode": "full", "value": "a"},
					map[string]interface{}{"path": "type", "mode": "full", "value": "b"},
				},
			},
		})), nil)
		require.NoError(t, err)
		assert.NotContains(t, string(out), `"type":"a"`)
		assert.NotContains(t, string(out), `"type":"b"`)
		assert.Contains(t, string(out), `"type":"c"`)
	})
	t.Run("type key with recursive false", func(t *testing.T) {
		input := `{"outer":{"type":"drop","inner":{"type":"drop"}}}`
		out, err := apply(t, input, ops(op(map[string]interface{}{
			"path": "outer", "mode": "prune_objects",
			"value": map[string]interface{}{"type": "drop", "recursive": false},
		})), nil)
		require.NoError(t, err)
		// root of target is not dropped, and recursive=false keeps nested.
		assert.Contains(t, string(out), "inner")
	})
	t.Run("invalid where type errors", func(t *testing.T) {
		_, err := apply(t, `{"a":[]}`, ops(op(map[string]interface{}{
			"path": "a", "mode": "prune_objects",
			"value": map[string]interface{}{"where": "not-an-object"},
		})), nil)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// return_error with "status" and "msg" keys
// ---------------------------------------------------------------------------

func TestReturnError_StatusAndMsgKeys(t *testing.T) {
	_, err := apply(t, `{"m":1}`, ops(op(map[string]interface{}{
		"mode": "return_error", "value": map[string]interface{}{"msg": "denied", "status": float64(429)},
	})), nil)
	require.Error(t, err)
	re, ok := AsParamOverrideReturnError(err)
	require.True(t, ok)
	assert.Equal(t, "denied", re.Message)
	assert.Equal(t, 429, re.StatusCode)
}

func priceDataWithUsePrice() types.PriceData {
	return types.PriceData{UsePrice: true, ModelPrice: 0.04}
}
