package common

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamFinalRequestCountBoundary 验证出站字段缺失、显式零、类型及协议隔离；t 仅操作未发请求的本地会话。
func TestStreamFinalRequestCountBoundary(t *testing.T) {
	for _, tc := range []struct {
		format types.RelayFormat // 最后一次请求转换确定的实际上游协议。
		body   string            // 最终出站 JSON，不被存入会话。
		want   int               // 预期候选下限，0 表示仅依赖已观察候选。
	}{
		{types.RelayFormatOpenAI, `{"n":1}`, 1},
		{types.RelayFormatOpenAI, `{"n":3}`, 3},
		{types.RelayFormatOpenAI, `{}`, 0},
		{types.RelayFormatOpenAI, `{"n":0}`, 0},
		{types.RelayFormatOpenAI, `{"n":null}`, 0},
		{types.RelayFormatOpenAI, `{"n":-1}`, 0},
		{types.RelayFormatOpenAI, `{"n":1.5}`, 0},
		{types.RelayFormatOpenAI, `{"n":"3"}`, 0},
		{types.RelayFormatOpenAI, `{"n":128}`, 128},
		{types.RelayFormatOpenAI, `{"n":129}`, 129},
		{types.RelayFormatOpenAI, `{"n":1e30}`, 0},
		{types.RelayFormatGemini, `{"generationConfig":{"candidateCount":3}}`, 3},
		{types.RelayFormatGemini, `{"generationConfig":{"candidate_count":3}}`, 3},
		{types.RelayFormatGemini, `{"generationConfig":{"candidateCount":1,"candidate_count":3}}`, 3},
		{types.RelayFormatGemini, `{"generationConfig":{"candidateCount":1,"candidate_count":null}}`, 1},
		{types.RelayFormatGemini, `{"generationConfig":{}}`, 0},
		{types.RelayFormatClaude, `{"n":3}`, 0},
		{types.RelayFormatOpenAIResponses, `{"n":3}`, 0},
	} {
		info := &RelayInfo{RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: tc.format, StreamSession: NewStreamSession(types.RelayFormatOpenAI)}
		info.StreamSession.ResponseGate = &StreamResponseGate{} // 真正发送前尚未收到 HTTP 200。
		info.StreamSession.ExpectedChoices = 2
		info.UpdateStreamExpectedChoices([]byte(tc.body))
		require.Equal(t, tc.want, info.StreamSession.ExpectedChoices, tc.body)
		require.False(t, info.StreamSession.Active(), "参数同步不提前激活响应门控")
	}
	var missing *RelayInfo
	missing.UpdateStreamExpectedChoices([]byte(`{"n":2}`))
	legacy := &RelayInfo{}
	legacy.UpdateStreamExpectedChoices([]byte(`{"n":2}`))
	require.Nil(t, legacy.StreamSession, "非流式/关闭开关不新建会话")
}

// TestStreamFinalImageCountBoundary 验证图片出站 n 的缺省、类型、数值边界和未安装会话隔离；t 不发起网络请求。
func TestStreamFinalImageCountBoundary(t *testing.T) {
	for _, tc := range []struct {
		body string // 参数覆盖后的完整 JSON，不留存在会话中。
		want int    // 图片默认至少一张，与普通聊天的候选数相互独立。
	}{
		{`{}`, 1},
		{`{"n":null}`, 1},
		{`{"n":0}`, 1},
		{`{"n":1}`, 1},
		{`{"n":3}`, 3},
		{`{"n":-1}`, 1},
		{`{"n":1.5}`, 1},
		{`{"n":"3"}`, 1},
		{`{"n":true}`, 1},
		{`{"n":1000000000}`, 1000000000},
		{`{"n":1000000001}`, 1},
		{`{"n":1e30}`, 1},
	} {
		for _, path := range []string{"n", "parameters.n"} {
			body := tc.body
			if path == "parameters.n" {
				body = `{"n":9,"parameters":` + body + `}` // 原生字段应覆盖无关的顶层计数。
			}
			info := &RelayInfo{StreamSession: NewStreamSession(types.RelayFormatOpenAI)}
			info.StreamSession.ResponseGate = &StreamResponseGate{}
			info.StreamSession.ExpectedImages = 2
			info.StreamSession.ExpectedChoices = 3
			info.UpdateStreamExpectedImages([]byte(body), path)
			require.Equal(t, tc.want, info.StreamSession.ExpectedImages, body)
			require.Equal(t, 3, info.StreamSession.ExpectedChoices)
			require.False(t, info.StreamSession.Active(), "出站计数不提前开放 HTTP 门控")
		}
	}
	var missing *RelayInfo
	missing.UpdateStreamExpectedImages([]byte(`{"n":2}`), "n")
	legacy := &RelayInfo{}
	legacy.UpdateStreamExpectedImages([]byte(`{"n":2}`), "n")
	require.Nil(t, legacy.StreamSession, "非流式/关闭开关不创建新会话")
}

// BenchmarkStreamFinalCandidateCount 衡量已有 JSON 查询的成本；b 控制小请求及带 1 MiB 正文的大请求迭代。
func BenchmarkStreamFinalCandidateCount(b *testing.B) {
	for _, size := range []int{32, 1 << 20} {
		body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("x", size) + `"}],"n":2}`)
		name := "small"
		if size > 32 {
			name = "1MiB"
		}
		b.Run(name, func(b *testing.B) {
			info := &RelayInfo{RelayFormat: types.RelayFormatOpenAI, StreamSession: NewStreamSession(types.RelayFormatOpenAI)}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				info.UpdateStreamExpectedChoices(body)
			}
		})
	}
}
