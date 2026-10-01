package proberouting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name, path, body string
		want             bool
	}{
		{"chat arbitrary", "/v1/chat/completions", `{"messages":[{"role":"user","content":"测试 abc 🐈"}]}`, true},
		{"responses screenshot", "/v1/responses", `{"model":"gpt-6-astra","input":[{"role":"user","content":"在吗"}],"instructions":"只回复一个字:好","max_output_tokens":8296}`, true},
		{"responses string", "/v1/responses", `{"input":"hello","instructions":"be brief"}`, true},
		{"responses blocks", "/v1/responses", `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`, true},
		{"claude", "/v1/messages", `{"system":[{"type":"text","text":"brief","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"ping"}]}]}`, true},
		{"cache independent", "/v1/chat/completions", `{"prompt_cache_key":"abc","prompt_cache_retention":"24h","messages":[{"role":"user","content":"hi"}]}`, true},
		{"empty tools", "/v1/chat/completions", `{"tools":[],"functions":null,"tool_choice":"none","messages":[{"role":"user","content":"hi"}]}`, true},
		{"instructions counted", "/v1/responses", `{"input":"hi","instructions":"` + strings.Repeat("字", 128) + `"}`, false},
		{"system counted", "/v1/chat/completions", `{"messages":[{"role":"system","content":"` + strings.Repeat("x", 128) + `"},{"role":"user","content":"hi"}]}`, false},
		{"assistant history", "/v1/chat/completions", `{"messages":[{"role":"assistant","content":"hi"},{"role":"user","content":"hi"}]}`, false},
		{"two users", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"},{"role":"user","content":"hi"}]}`, false},
		{"late system", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"},{"role":"system","content":"hi"}]}`, false},
		{"image", "/v1/chat/completions", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`, false},
		{"tool definitions", "/v1/responses", `{"input":"hi","tools":[{"type":"web_search"}]}`, false},
		{"tool auto", "/v1/responses", `{"input":"hi","tool_choice":"auto"}`, false},
		{"claude none", "/v1/messages", `{"tool_choice":{"type":"none"},"messages":[{"role":"user","content":"hi"}]}`, true},
		{"tool result", "/v1/responses", `{"input":[{"type":"function_call_output","output":"hi"}]}`, false},
		{"previous response", "/v1/responses", `{"input":"hi","previous_response_id":"resp_1"}`, false},
		{"conversation", "/v1/responses", `{"input":"hi","conversation":{"id":"a"}}`, false},
		{"prompt reference", "/v1/responses", `{"input":"hi","prompt":{"id":"p"}}`, false},
		{"structured chat", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`, false},
		{"structured responses", "/v1/responses", `{"input":"hi","text":{"format":{"type":"json_schema"}}}`, false},
		{"structured claude legacy", "/v1/messages", `{"messages":[{"role":"user","content":"hi"}],"output_format":{"type":"json_schema","schema":{}}}`, false},
		{"audio output", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"modalities":["text","audio"]}`, false},
		{"text modality", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"modalities":["text"]}`, true},
		{"plain text", "/v1/responses", `{"input":"hi","text":{"format":{"type":"text"}}}`, true},
		{"multi output", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"n":2}`, false},
		{"unknown block", "/v1/messages", `{"messages":[{"role":"user","content":[{"type":"future","text":"hi"}]}]}`, false},
		{"blank", "/v1/responses", `{"input":" \n\t"}`, false},
		{"empty", "/v1/responses", `{"input":[]}`, false},
		{"invalid", "/v1/responses", `{`, false},
		{"mixed protocol", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"instructions":"hidden context"}`, false},
		{"unsupported", "/v1/responses/compact", `{"input":"hi"}`, false},
		{"escaped", "/v1/responses", `{"input":"\u4f60\u597d"}`, true},
		{"oversize", "/v1/responses", `{"input":"hi","metadata":"` + strings.Repeat("x", MaxBodyBytes) + `"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, matched := Classify(tt.path, []byte(tt.body), 128)
			require.Equal(t, tt.want, matched)
		})
	}
}

func TestClassifyLengthBoundaries(t *testing.T) {
	for _, n := range []int{127, 128, 129} {
		for _, char := range []string{"x", "中", "🐈", " "} {
			text := "a" + strings.Repeat(char, n-1)
			body, err := common.Marshal(map[string]any{"input": text})
			require.NoError(t, err)
			count, matched := Classify("/v1/responses", body, 128)
			require.Equal(t, n <= 128, matched)
			if matched {
				require.Equal(t, n, count)
			}
		}
	}
	for _, n := range []int{MaxBodyBytes - 1, MaxBodyBytes, MaxBodyBytes + 1} {
		base := `{"input":"hi"}`
		_, matched := Classify("/v1/responses", []byte(base+strings.Repeat(" ", n-len(base))), 128)
		require.Equal(t, n <= MaxBodyBytes, matched)
	}
}

func BenchmarkClassify(b *testing.B) {
	for _, item := range []struct{ name, body string }{
		{"probe", `{"input":[{"role":"user","content":"在吗"}],"instructions":"只回复一个字:好","max_output_tokens":8296}`},
		{"long", `{"input":"` + strings.Repeat("a", 4096) + `"}`},
		{"oversize", strings.Repeat(" ", MaxBodyBytes+1)},
		{"boundary16k", `{"input":"hi","metadata":"` + strings.Repeat("x", MaxBodyBytes-28) + `"}`},
		{"tools", `{"input":"hi","tools":[{"type":"web_search"}]}`},
	} {
		b.Run(item.name, func(b *testing.B) {
			body := []byte(item.body)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Classify("/v1/responses", body, 128)
			}
		})
	}
}
