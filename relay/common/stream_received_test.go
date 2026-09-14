package common_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestReceivedStreamNativeText(t *testing.T) {
	for _, tc := range []struct {
		name, event, raw string
		channel          int
	}{
		{"chat", "", `{"choices":[{"delta":{"content":"hello"}}]}`, constant.ChannelTypeOpenAI},
		{"responses", "", `{"type":"response.output_text.delta","delta":"hello"}`, constant.ChannelTypeOpenAI},
		{"gemini", "", `{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`, constant.ChannelTypeGemini},
		{"cohere", "", `{"event_type":"text-generation","text":"hello"}`, constant.ChannelTypeCohere},
		{"dify", "", `{"event":"message","answer":"hello"}`, constant.ChannelTypeDify},
		{"baidu", "", `{"result":"hello"}`, constant.ChannelTypeBaidu},
		{"tencent", "", `{"Choices":[{"Delta":{"Content":"hello"}}]}`, constant.ChannelTypeTencent},
		{"coze", "conversation.message.delta", `{"content":"hello"}`, constant.ChannelTypeCoze},
		{"xunfei", "", `{"payload":{"choices":{"text":[{"content":"hello"}]}}}`, constant.ChannelTypeXunfei},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, StreamSession: relaycommon.NewStreamSession(types.RelayFormatOpenAI)}
			s := info.StreamSession
			s.ChannelType = tc.channel
			service.InitStreamReceivedEstimator(info)
			require.NoError(t, s.ObserveEvent(tc.event, []byte(tc.raw)))
			state := s.Snapshot()
			require.True(t, state.ReceivedResponse)
			require.False(t, state.Effective)
			require.Equal(t, service.EstimateTokenByModel("gpt-4o", "hello"), state.ReceivedOutput)
		})
	}
}

func TestReceivedStreamRetryAndCancellation(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, StreamSession: relaycommon.NewStreamSession(types.RelayFormatOpenAI)}
	s := info.StreamSession
	service.InitStreamReceivedEstimator(info)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.BindContext(ctx)
	s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.NoError(t, s.ObserveEvent("", []byte(`{"choices":[{"delta":{"content":"old answer"}}]}`)))
	s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	require.False(t, s.Snapshot().ReceivedResponse)
	require.Zero(t, s.Snapshot().ReceivedOutput)
	require.NoError(t, s.ObserveEvent("", []byte(`{"choices":[{"delta":{"content":"new"}}]}`)))
	require.Equal(t, service.EstimateTokenByModel("gpt-4o", "new"), s.Snapshot().ReceivedOutput)
	cancel()
	require.ErrorIs(t, s.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":999}}`)), context.Canceled)
	require.Empty(t, s.Snapshot().Evidence)
}

func TestReceivedTranscriptCompletionDoesNotRepeatDeltas(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, StreamSession: relaycommon.NewStreamSession(types.RelayFormatOpenAI)}
	service.InitStreamReceivedEstimator(info)
	for _, frame := range []string{`{"type":"transcript.text.delta","delta":"hello "}`, `{"type":"transcript.text.delta","delta":"world"}`, `{"type":"transcript.text.done","text":"hello world"}`} {
		require.NoError(t, info.StreamSession.ObserveEvent("", []byte(frame)))
	}
	require.Equal(t, service.EstimateTokenByModel("gpt-4o", "hello world"), info.StreamSession.Snapshot().ReceivedOutput)
}

func TestReceivedResponsePartialTool(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, StreamSession: relaycommon.NewStreamSession(types.RelayFormatOpenAI)}
	service.InitStreamReceivedEstimator(info)
	for _, frame := range []string{`{"type":"response.output_item.added","item":{"type":"function_call","id":"i1","name":"lookup"}}`, `{"type":"response.function_call_arguments.delta","item_id":"i1","delta":"{"}`} {
		require.NoError(t, info.StreamSession.ObserveEvent("", []byte(frame)))
	}
	require.Equal(t, service.EstimateTokenByModel("gpt-4o", "lookup{"), info.StreamSession.Snapshot().ReceivedOutput)
	require.False(t, info.StreamSession.Snapshot().Effective)
}

func TestReceivedStreamToolDedupAndPartial(t *testing.T) {
	for _, frames := range [][]string{
		{`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"{"}}]}}]}`},
		{`{"type":"response.output_item.added","item":{"type":"function_call","id":"i1","name":"lookup"}}`, `{"type":"response.function_call_arguments.delta","item_id":"i1","delta":"{}"}`, `{"type":"response.function_call_arguments.done","item_id":"i1","call_id":"c1","name":"lookup","arguments":"{}"}`, `{"type":"response.output_item.done","item":{"type":"function_call","id":"i1","call_id":"c1","name":"lookup","arguments":"{}"}}`},
		{`{"type":"response.function_call_arguments.done","item_id":"i1","call_id":"c1","name":"lookup","arguments":"{}"}`, `{"type":"response.output_item.done","item":{"type":"function_call","id":"i1","call_id":"c1","name":"lookup","arguments":"{}"}}`},
	} {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, StreamSession: relaycommon.NewStreamSession(types.RelayFormatOpenAI)}
		service.InitStreamReceivedEstimator(info)
		for _, frame := range frames {
			require.NoError(t, info.StreamSession.ObserveEvent("", []byte(frame)))
		}
		text := "lookup{}"
		if len(frames) == 1 {
			text = "lookup{"
		}
		require.Equal(t, service.EstimateTokenByModel("gpt-4o", text), info.StreamSession.Snapshot().ReceivedOutput)
	}
}

func BenchmarkStreamReceivedEvent(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "without_received_estimator"
		if enabled {
			name = "with_received_estimator"
		}
		b.Run(name, func(b *testing.B) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, StreamSession: relaycommon.NewStreamSession(types.RelayFormatOpenAI)}
			if enabled {
				service.InitStreamReceivedEstimator(info)
			}
			data := []byte(`{"choices":[{"index":0,"delta":{"content":"A short streaming response with useful content."}}]}`)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := info.StreamSession.ObserveEvent("", data); err != nil {
					require.NoError(b, err)
				}
			}
		})
	}
}
