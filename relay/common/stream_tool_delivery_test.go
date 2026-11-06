package common

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamCustomToolDelivery(t *testing.T) {
	const added = `{"type":"response.output_item.added","item":{"id":"custom1","call_id":"call1","type":"custom_tool_call","name":"apply_patch","input":"","status":"in_progress"}}`
	const delta = `{"type":"response.custom_tool_call_input.delta","item_id":"custom1","delta":"print('hello')"}`
	const done = `{"type":"response.custom_tool_call_input.done","item_id":"custom1","input":"print('hello')"}`
	const item = `{"type":"response.output_item.done","item":{"id":"custom1","call_id":"call1","type":"custom_tool_call","name":"apply_patch","input":"print('hello')","status":"completed"}}`
	for _, tc := range []struct {
		name   string
		frames []string
		want   string
	}{
		{"partial", []string{added, delta}, ""},
		{"raw input done", []string{added, delta, done}, "apply_patchprint('hello')"},
		{"raw item done", []string{item}, "apply_patchprint('hello')"},
		{"done then item dedup", []string{added, delta, done, item, done}, "apply_patchprint('hello')"},
		{"item then done dedup", []string{added, item, done, item}, "apply_patchprint('hello')"},
		{"empty raw input", []string{added, `{"type":"response.custom_tool_call_input.done","item_id":"custom1","input":""}`}, "apply_patch"},
		{"missing name", []string{done}, ""},
		{"missing input", []string{added, `{"type":"response.custom_tool_call_input.done","item_id":"custom1"}`}, ""},
		{"invalid input type", []string{added, `{"type":"response.custom_tool_call_input.done","item_id":"custom1","input":42}`}, ""},
		{"incomplete item", []string{`{"type":"response.output_item.done","item":{"id":"custom1","type":"custom_tool_call","name":"apply_patch","input":"code","status":"incomplete"}}`}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAIResponses)
			var text strings.Builder
			for _, frame := range tc.frames {
				s.CommitDelivery([]byte(frame))
				text.WriteString(s.TakeDeliveredText())
			}
			require.Equal(t, tc.want, text.String())
			require.Equal(t, tc.want != "", s.Snapshot().Effective)
		})
	}
}

func TestStreamCustomToolReceivedFragments(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAIResponses)
	s.receivedOnly = true
	var received strings.Builder
	for _, frame := range []string{
		`{"type":"response.output_item.added","item":{"id":"custom1","call_id":"call1","type":"custom_tool_call","name":"exec","input":""}}`,
		`{"type":"response.custom_tool_call_input.delta","item_id":"custom1","delta":"print("}`,
		`{"type":"response.custom_tool_call_input.delta","item_id":"custom1","delta":"'hello')"}`,
		`{"type":"response.custom_tool_call_input.done","item_id":"custom1","input":"print('hello')"}`,
		`{"type":"response.output_item.done","item":{"id":"custom1","call_id":"call1","type":"custom_tool_call","name":"exec","input":"print('hello')","status":"completed"}}`,
	} {
		s.CommitDelivery([]byte(frame))
		received.WriteString(s.TakeDeliveredText())
	}
	require.Equal(t, "execprint('hello')", received.String(), "completion snapshots must not repeat generated input")
	require.Empty(t, s.tools)
}

// TestStreamToolCompletionDedup 用 t 验证两类完成事件和别名补齐只计量一次，非法候选不占用已交付身份。
func TestStreamToolCompletionDedup(t *testing.T) {
	const item = `{"type":"response.output_item.done","item":{"type":"function_call","id":"item1","call_id":"call1","name":"lookup","arguments":"{\"x\":1}"}}`
	const args = `{"type":"response.function_call_arguments.done","item_id":"item1","call_id":"call1","name":"lookup","arguments":"{\"x\":1}"}`
	const itemOnly = `{"type":"response.output_item.done","item":{"type":"function_call","id":"item1","name":"lookup","arguments":"{\"x\":1}"}}`
	const callOnly = `{"type":"response.function_call_arguments.done","call_id":"call1","name":"lookup","arguments":"{\"x\":1}"}`
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIRealtime, types.RelayFormatOpenAIResponses} {
		for _, tc := range []struct {
			name   string   // 场景名称。
			frames []string // 已成功写出的完成事件。
		}{
			{"args first", []string{args, item, args}},
			{"item first", []string{item, args, item}},
			{"join item alias", []string{itemOnly, args, callOnly}},
			{"join call alias", []string{callOnly, item, itemOnly}},
			{"missing name", []string{`{"type":"response.function_call_arguments.done","item_id":"item1","call_id":"call1","arguments":"{\"x\":1}"}`, item, args}},
			{"invalid args", []string{`{"type":"response.function_call_arguments.done","item_id":"item1","call_id":"call1","name":"lookup","arguments":"{"}`, item, args}},
		} {
			t.Run(string(format)+"/"+tc.name, func(t *testing.T) {
				s := NewStreamSession(format)
				var delivered string
				for _, frame := range tc.frames {
					s.CommitDelivery([]byte(frame))
					delivered += s.TakeDeliveredText()
				}
				require.Equal(t, `lookup{"x":1}`, delivered)
				require.True(t, s.Snapshot().Effective)
			})
		}
	}
}

// TestStreamToolCompletionBounds 用 t 覆盖容量边界、固定大小身份、不同工具及新成功响应清零。
func TestStreamToolCompletionBounds(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAIResponses)
	s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.Nil(t, s.toolDeliveries, "非工具请求不分配去重表")
	for i := 0; i < 129; i++ {
		frame := fmt.Sprintf(`{"type":"response.function_call_arguments.done","item_id":"item%d","call_id":"call%d","name":"lookup","arguments":"{}"}`, i, i)
		s.CommitDelivery([]byte(frame))
		require.Equal(t, "lookup{}", s.TakeDeliveredText(), "不同 ID 即使内容相同也分别计量")
		s.CommitDelivery([]byte(frame))
		require.Empty(t, s.TakeDeliveredText())
		require.Len(t, s.toolDeliveries.seen, min(i+1, 128)*2)
	}
	first := []byte(`{"type":"response.output_item.done","item":{"type":"function_call","id":"item0","call_id":"call0","name":"lookup","arguments":"{}"}}`)
	s.CommitDelivery(first)
	require.Equal(t, "lookup{}", s.TakeDeliveredText(), "已淘汰身份不再占用连接状态")
	require.Len(t, s.toolDeliveries.seen, 256)
	s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.Nil(t, s.toolDeliveries)
	s.CommitDelivery(first)
	require.Equal(t, "lookup{}", s.TakeDeliveredText(), "SDK 新成功响应重新计量")
	longID := strings.Repeat("id", 100_000)
	longFrame := []byte(fmt.Sprintf(`{"type":"response.function_call_arguments.done","item_id":%q,"call_id":%q,"name":"lookup","arguments":"{}"}`, longID, longID))
	s.CommitDelivery(longFrame)
	require.Equal(t, "lookup{}", s.TakeDeliveredText())
	s.CommitDelivery(longFrame)
	require.Empty(t, s.TakeDeliveredText())
	require.Len(t, s.toolDeliveries.seen, 4, "长 ID 只保存定长摘要")
}

// TestStreamToolCompletionUnidentified 用 t 验证缺少共同 ID 不按参数猜测合并，空参数仍按完整空对象计量。
func TestStreamToolCompletionUnidentified(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAIResponses)
	for i := 0; i < 2; i++ {
		s.CommitDelivery([]byte(`{"type":"response.output_item.done","item":{"type":"function_call","name":"lookup","arguments":""}}`))
		require.Equal(t, "lookup{}", s.TakeDeliveredText())
		require.Nil(t, s.toolDeliveries)
	}
	for _, frame := range []string{
		`{"type":"response.output_item.done","item":{"type":"function_call","id":"same","name":"lookup","arguments":"{}"}}`,
		`{"type":"response.function_call_arguments.done","call_id":"same","name":"lookup","arguments":"{}"}`,
	} {
		s.CommitDelivery([]byte(frame))
		require.Equal(t, "lookup{}", s.TakeDeliveredText(), "不同命名空间的 ID 字面相同不作为共同身份")
	}
}

// TestStreamToolCompletionDeliveryGate 用 t 验证实际 SSE 帧均透传，估算只在成功刷新后去重提交。
func TestStreamToolCompletionDeliveryGate(t *testing.T) {
	const frames = "event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"i\",\"call_id\":\"c\",\"name\":\"lookup\",\"arguments\":\"{}\"}\n\nevent: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"i\",\"call_id\":\"c\",\"name\":\"lookup\",\"arguments\":\"{}\"}}\n\n"
	for _, fail := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		s := NewStreamSession(types.RelayFormatOpenAIResponses)
		underlying := c.Writer
		if fail {
			underlying = streamFlushFailure{underlying}
		}
		var delivered string
		w := NewStreamWriter(underlying, s, func(text string, _ int) int {
			delivered += text // 捕获刷新回调收到的成功交付候选，不在夹具中执行真实 token 估算。
			return len(text)
		})
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte(frames))
		require.NoError(t, err)
		require.Nil(t, s.toolDeliveries)
		err = w.FlushError()
		if fail {
			require.Error(t, err)
			require.Nil(t, s.toolDeliveries)
			require.Empty(t, delivered)
		} else {
			require.NoError(t, err)
			require.Equal(t, "lookup{}", delivered)
			require.Len(t, s.toolDeliveries.seen, 2)
		}
		require.Equal(t, frames, rec.Body.String(), "去重只影响估算，不删改任何完成事件")
	}
}

func TestResponsesOutputItemDeliveryRequiresCompletedStatus(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIRealtime} {
		for _, kind := range []string{"function_call", "image_generation_call"} {
			for _, tc := range []struct {
				status string
				valid  bool
			}{
				{"", true},
				{`,"status":"completed"`, true},
				{`,"status":"incomplete"`, false},
				{`,"status":"in_progress"`, false},
				{`,"status":"failed"`, false},
				{`,"status":"cancelled"`, false},
				{`,"status":""`, false},
				{`,"status":null`, false},
				{`,"status":1`, false},
			} {
				t.Run(string(format)+"/"+kind+"/"+tc.status, func(t *testing.T) {
					s := NewStreamSession(format)
					frame := fmt.Sprintf(`{"type":"response.output_item.done","item":{"id":"item1","call_id":"call1","type":%q,"name":"lookup","arguments":"{}","result":"aGk="%s}}`, kind, tc.status)
					s.CommitDelivery([]byte(frame))
					require.Equal(t, tc.valid, s.Snapshot().Effective)
					if kind == "function_call" && tc.valid {
						require.Equal(t, "lookup{}", s.TakeDeliveredText())
					} else {
						require.Empty(t, s.TakeDeliveredText())
					}
					if !tc.valid {
						completed := fmt.Sprintf(`{"type":"response.output_item.done","item":{"id":"item1","call_id":"call1","type":%q,"name":"lookup","arguments":"{}","result":"aGk=","status":"completed"}}`, kind)
						s.CommitDelivery([]byte(completed))
						require.True(t, s.Snapshot().Effective, "rejected partial items must not suppress later completion")
					}
				})
			}
		}
	}
}

func TestIncompleteResponsesToolKeepsReceivedEstimate(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAIResponses)
	s.SetReceivedEstimator(func() func(string, int) int {
		return func(text string, _ int) int { return len(text) }
	})
	frame := []byte(`{"type":"response.output_item.done","item":{"id":"item1","call_id":"call1","type":"function_call","name":"lookup","arguments":"{}","status":"incomplete"}}`)
	require.NoError(t, s.ObserveEvent("", frame))
	s.CommitDelivery(frame)
	require.Positive(t, s.Snapshot().ReceivedOutput, "received tool output is still available for client-disconnect accounting")
	require.False(t, s.Snapshot().Effective)
	require.Empty(t, s.TakeDeliveredText())
}
