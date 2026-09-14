package common

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"unsafe"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestStreamToolNameOwnsStorage 用 t 验证缓存名称独立持有字节，不通过 JSON 子串保留整帧；不依赖 GC 时机。
func TestStreamToolNameOwnsStorage(t *testing.T) {
	large := `{"name":"lookup","padding":"` + strings.Repeat("x", 1<<20) + `"}`
	name := gjson.Parse(large).Get("name").String()
	s := NewStreamSession(types.RelayFormatOpenAI)
	require.False(t, s.toolLocked("tool", name, "{", false, false))
	require.Equal(t, name, s.tools["tool"].name.String())
	require.False(t, unsafe.StringData(name) == unsafe.StringData(s.tools["tool"].name.String()), "工具名应只保留自身字节")
	require.Equal(t, len(name)+1, s.toolBytes)
	require.True(t, s.toolLocked("tool", "", "}", true, false))
	require.Equal(t, "lookup{}", s.text.String())
	require.Zero(t, s.toolBytes)
	require.Empty(t, s.tools)
}

// TestStreamOriginalErrorPayloadOwnership 用 t 验证错误载荷独立复制、快照隔离、首错优先和 SDK 新成功响应清理。
func TestStreamOriginalErrorPayloadOwnership(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAIRealtime)
	s.ObserveWebSocketHandshake(&http.Response{StatusCode: http.StatusSwitchingProtocols}, nil)
	require.NoError(t, s.ObserveEvent("", []byte(`{"usage":{"input_tokens":10,"output_tokens":0}}`)))
	raw := []byte(" \n" + `{"type":"error","error":{"type":"server_error","message":"original"},"usage":"invalid"}` + "\t ")
	want := bytes.Clone(raw)
	require.Error(t, s.ObserveEvent("", raw))
	raw[0] = 'x'
	snapshot := s.Snapshot()
	require.Equal(t, want, snapshot.ErrorPayload)
	require.Empty(t, snapshot.ErrorFrame)
	require.Equal(t, map[string]int{"input_tokens": 10, "output_tokens": 0}, snapshot.Evidence)
	snapshot.ErrorPayload[0] = 'y'
	snapshot.Evidence["input_tokens"] = 999
	require.Error(t, s.ObserveEvent("", []byte(`{"type":"error","usage":{"input_tokens":999}}`)))
	require.Equal(t, want, s.Snapshot().ErrorPayload)
	require.Equal(t, 10, s.Snapshot().Evidence["input_tokens"])
	s.ObserveWebSocketHandshake(&http.Response{StatusCode: http.StatusSwitchingProtocols}, nil)
	require.Empty(t, s.Snapshot().ErrorPayload)
	require.Empty(t, s.Snapshot().Evidence)
	require.Empty(t, s.Snapshot().Reason)

	s = NewStreamSession(types.RelayFormatClaude)
	frame := []byte("event: error\r\ndata: " + `{"type":"error","error":{"type":"api_error"},"usage":"invalid"}` + "\r\n\r\n")
	require.Error(t, s.ObserveFrame(frame))
	require.Equal(t, frame, s.Snapshot().ErrorFrame)
	require.Empty(t, s.Snapshot().ErrorPayload, "完整 SSE 帧替代临时载荷，只保留一个表示")
	frame[0] = 'x'
	require.Equal(t, byte('e'), s.Snapshot().ErrorFrame[0])
}

// TestStreamOriginalErrorPayloadBoundaries 用 t 覆盖大小边界及未接管、取消、可恢复错误不占终止载荷的分支。
func TestStreamOriginalErrorPayloadBoundaries(t *testing.T) {
	for _, size := range []int{MaxStreamFrameBytes - 1, MaxStreamFrameBytes, MaxStreamFrameBytes + 1} {
		s := NewStreamSession(types.RelayFormatOpenAIRealtime)
		prefix, suffix := `{"type":"error","error":{"type":"server_error","message":"`, `"}}`
		raw := []byte(prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix)
		err := s.ObserveEvent("", raw)
		if size > MaxStreamFrameBytes {
			require.Error(t, err)
			require.Empty(t, s.Snapshot().ErrorPayload)
		} else {
			require.NoError(t, err)
			require.Len(t, s.Snapshot().ErrorPayload, size)
		}
	}
	for _, kind := range []string{"inactive", "canceled", "recoverable", "invalid json"} {
		t.Run(kind, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAIRealtime)
			raw := []byte(`{"type":"error","error":{"type":"server_error","message":"original"},"usage":"invalid"}`)
			switch kind {
			case "inactive":
				s.ResponseGate = &StreamResponseGate{}
			case "canceled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				s.BindContext(ctx)
			case "recoverable":
				raw = []byte(`{"type":"error","error":{"type":"invalid_request_error"},"usage":"invalid"}`)
			case "invalid json":
				raw = []byte(`{"type":"error","error":`)
			}
			_ = s.ObserveEvent("", raw)
			require.Empty(t, s.Snapshot().ErrorPayload)
			require.Empty(t, s.Snapshot().ErrorFrame)
		})
	}
}
