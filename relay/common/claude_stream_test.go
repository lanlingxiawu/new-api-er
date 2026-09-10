package common

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResponseCaptureBoundaries 覆盖 0、1、1024、2048、2049 和大响应，验证前后截取边界、字节顺序及观察长度。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestResponseCaptureBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 1024, 2048, 2049, 9999} {
		b := make([]byte, size)
		for i := range b {
			b[i] = byte(i % 251)
		}
		var capture ResponseCapture
		for i := 0; i < len(b); i += 37 {
			end := min(i+37, len(b))
			_, err := capture.Write(b[i:end])
			require.NoError(t, err)
		}
		head, tail := capture.Bytes()
		require.Equal(t, int64(size), capture.Observed)
		if size <= 2048 {
			require.Equal(t, b, append(head, tail...))
		} else {
			require.True(t, bytes.Equal(b[:1024], head))
			require.True(t, bytes.Equal(b[size-1024:], tail))
		}
	}
}

// TestClaudeBillingDecision 遍历用户取消/上游异常、确认用量和有效内容组合，并检查正常结束的估算规则。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeBillingDecision(t *testing.T) {
	for _, tc := range []struct {
		client, confirmed, effective bool   // 依次表示用户断开、上游已有确认用量、已有有效交付。
		want                         string // 预期计费来源：upstream、estimated 或 none。
	}{
		{true, true, false, "upstream"}, {true, true, true, "upstream"}, {true, false, true, "none"}, {true, false, false, "none"},
		{false, true, true, "upstream"}, {false, true, false, "none"}, {false, false, true, "estimated"}, {false, false, false, "none"},
	} {
		s := ClaudeStreamOutcome{ClientGone: tc.client, ConfirmedUsage: tc.confirmed, EffectiveContent: tc.effective, Failed: true}
		require.Equal(t, tc.want, s.SelectUsageSource())
	}
	for _, confirmed := range []bool{false, true} {
		s := ClaudeStreamOutcome{ConfirmedUsage: confirmed}
		want := "estimated"
		if confirmed {
			want = "upstream"
		}
		require.Equal(t, want, s.SelectUsageSource(), "normal completion retains legacy estimation even without effective content")
	}
}
