package relay

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestUnifiedStreamImageFinalRequest 捕获实际图片出站请求，覆盖覆盖增减、删除、缺省、透传和重试；t 管理本地 503 夹具。
func TestUnifiedStreamImageFinalRequest(t *testing.T) {
	for _, edits := range []bool{false, true} {
		for _, passthrough := range []bool{false, true} {
			for _, hasN := range []bool{false, true} {
				for _, count := range []int{0, 1, 3} {
					received := make(chan string, 1)
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, _ := io.ReadAll(r.Body)
						received <- string(body)
						w.WriteHeader(503)
						_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
					}))
					t.Cleanup(srv.Close)
					raw := `{"model":"gpt-image-1","stream":true,"prompt":"fixture"}`
					if hasN {
						raw = `{"model":"gpt-image-1","stream":true,"prompt":"fixture","n":2}`
					}
					req := &dto.ImageRequest{}
					require.NoError(t, common.UnmarshalJsonStr(raw, req))
					path, mode := "/v1/images/generations", relayconstant.RelayModeImagesGenerations
					if edits {
						path, mode = "/v1/images/edits", relayconstant.RelayModeImagesEdits
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest("POST", path, strings.NewReader(raw))
					c.Request.Header.Set("Content-Type", "application/json")
					common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
					common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
					common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-image-1")
					common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
					info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: mode, RequestURLPath: path, OriginModelName: "gpt-image-1", Request: req, ChannelMeta: &relaycommon.ChannelMeta{}}
					for attempt := 0; attempt < 2; attempt++ {
						want := count
						if attempt == 1 {
							want = 3 - count
						}
						op := map[string]any{"mode": "set", "path": "n", "value": want}
						if want == 0 {
							op["mode"] = "delete"
						}
						common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{op}})
						service.BeginStreamAttempt(c, info)
						err := ImageHelper(c, info)
						require.NotNil(t, err)
						require.Equal(t, 503, err.StatusCode)
						body := <-received
						if passthrough {
							require.Equal(t, raw, body)
							want = 0
							if hasN {
								want = 2
							}
						}
						require.Equal(t, int64(want), gjson.Get(body, "n").Int(), body)
						require.Equal(t, max(1, want), info.StreamSession.ExpectedImages, body)
						require.False(t, info.StreamSession.Active())
						require.Nil(t, info.StreamResult)
						info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
						require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"image_generation.completed","b64_json":"aGk="}`)))
						require.Equal(t, want <= 1, info.StreamSession.ProtocolComplete())
					}
				}
			}
		}
	}
}

// TestUnifiedStreamXAIImageFinalRequest 验证 xAI 自有 DTO 仍按出站标准 n 同步约束；t 覆盖删除、null、增减、透传和重试。
func TestUnifiedStreamXAIImageFinalRequest(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(strconv.FormatBool(passthrough), func(t *testing.T) {
			received := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received <- string(body)
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
			}))
			defer srv.Close()
			raw := `{"model":"grok-image","stream":true,"prompt":"fixture","n":2}`
			req := &dto.ImageRequest{}
			require.NoError(t, common.UnmarshalJsonStr(raw, req))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeXai)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "grok-image")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesGenerations, RequestURLPath: "/v1/images/generations", OriginModelName: "grok-image", Request: req, ChannelMeta: &relaycommon.ChannelMeta{}}
			for _, value := range []any{1, 3, nil, "delete"} {
				op := map[string]any{"mode": "set", "path": "n", "value": value}
				if value == "delete" {
					op["mode"] = "delete"
				}
				common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{op}})
				service.BeginStreamAttempt(c, info)
				apiErr := ImageHelper(c, info)
				require.NotNil(t, apiErr)
				require.Equal(t, 503, apiErr.StatusCode)
				body := <-received
				if passthrough {
					require.Equal(t, raw, body)
				}
				want := max(1, int(gjson.Get(body, "n").Int()))
				require.Equal(t, want, info.StreamSession.ExpectedImages, body)
				require.False(t, info.StreamSession.Active())
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
				response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://fixture.invalid/image"}]}`))}
				info.StreamSession.ObserveHTTP(response)
				_, readErr := io.ReadAll(response.Body)
				require.Equal(t, want == 1, readErr == nil, body)
			}
		})
	}
}

// TestUnifiedStreamImageMultipartCount 验证表单图片编辑保留原 n 和文件，JSON 覆盖不改变此路径；t 使用内存表单和本地 503 响应。
func TestUnifiedStreamImageMultipartCount(t *testing.T) {
	var raw bytes.Buffer
	form := multipart.NewWriter(&raw)
	for key, value := range map[string]string{"model": "gpt-image-1", "stream": "true", "n": "2", "prompt": "fixture"} {
		require.NoError(t, form.WriteField(key, value))
	}
	part, err := form.CreateFormFile("image", "fixture.png")
	require.NoError(t, err)
	_, err = io.WriteString(part, "fixture-image")
	require.NoError(t, err)
	require.NoError(t, form.Close())
	type formResult struct {
		n, image string // 实际出站张数和图片字段，仅在本地夹具中读取。
		err      error  // 测试服务解析表单的错误，交给测试所有者断言。
	}
	received := make(chan formResult, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := formResult{}
		result.err = r.ParseMultipartForm(1 << 20)
		if result.err == nil {
			result.n = r.FormValue("n")
			file, _, fileErr := r.FormFile("image")
			result.err = fileErr
			if fileErr == nil {
				data, readErr := io.ReadAll(file)
				_ = file.Close()
				result.image, result.err = string(data), readErr
			}
		}
		received <- result
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
	}))
	defer srv.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", &raw)
	c.Request.Header.Set("Content-Type", form.FormDataContentType())
	require.NoError(t, c.Request.ParseMultipartForm(1<<20))
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-image-1")
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{map[string]any{"mode": "set", "path": "n", "value": 3}}})
	n := uint(2)
	info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesEdits, RequestURLPath: "/v1/images/edits", OriginModelName: "gpt-image-1", Request: &dto.ImageRequest{Model: "gpt-image-1", N: &n}, ChannelMeta: &relaycommon.ChannelMeta{}}
	service.BeginStreamAttempt(c, info)
	apiErr := ImageHelper(c, info)
	require.NotNil(t, apiErr)
	require.Equal(t, 503, apiErr.StatusCode)
	got := <-received
	require.NoError(t, got.err)
	require.Equal(t, "2", got.n)
	require.Equal(t, "fixture-image", got.image)
	require.Equal(t, 2, info.StreamSession.ExpectedImages)
	require.False(t, info.StreamSession.Active())
}
