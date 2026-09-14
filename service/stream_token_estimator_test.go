package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStreamTokenEstimatorBoundaries 验证 t 中所有切分边界、空批次、模型延迟初始化及首次权重冻结。
func TestStreamTokenEstimatorBoundaries(t *testing.T) {
	for _, model := range []string{"gpt-4o", "CLAUDE-test", "Gemini-test", "unknown"} {
		for _, full := range []string{"", "hello", "123456", "ab12Cd34", "日中かな한🙂∑@://..\t\n", "a b", "éßЯ٣"} {
			runes := []rune(full)
			for split := 0; split <= len(runes); split++ {
				t.Run(fmt.Sprintf("%s/%s/%d", model, full, split), func(t *testing.T) {
					var e StreamTokenEstimator
					require.Zero(t, e.Add("other", ""))
					total := e.Add(model, string(runes[:split]))
					total += e.Add(model, string(runes[split:]))
					require.Equal(t, EstimateTokenByModel(model, full), total)
					require.Zero(t, e.Add(model, ""))
				})
			}
		}
	}
	var e StreamTokenEstimator
	total := e.Add("claude", "中") + e.Add("gemini", "文")
	require.Equal(t, EstimateTokenByModel("claude", "中文"), total, "一轮中途模型字段变化不改变已经采用的权重")
}

// BenchmarkStreamTokenEstimator 测量 b 的固定大小增量状态开销，不包含网络或完整文本累计缓存。
func BenchmarkStreamTokenEstimator(b *testing.B) {
	var e StreamTokenEstimator
	e.Add("gpt-4o", "warmup ")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Add("gpt-4o", "A short streaming response with useful content. ")
	}
}
