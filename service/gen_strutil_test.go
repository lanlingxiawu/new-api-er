package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// str.go
// ---------------------------------------------------------------------------

func TestStrutil_SundaySearch(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		pattern string
		want    bool
	}{
		{"match at start", "hello world", "hello", true},
		{"match at end", "hello world", "world", true},
		{"match in middle", "abcXYZdef", "XYZ", true},
		{"repeated overlap match", "abcabcabc", "cab", true},
		{"no match same length class", "hello world", "xyzuv", false},
		{"no match short pattern", "hello world", "zzz", false},
		{"pattern longer than text", "hi", "hello", false},
		{"empty pattern matches", "hello", "", true},
		{"empty text non-empty pattern", "", "a", false},
		{"both empty", "", "", true},
		{"full string equals pattern", "exact", "exact", true},
		{"single char present", "abcdef", "d", true},
		{"single char absent", "abcdef", "z", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SundaySearch(tc.text, tc.pattern))
		})
	}
}

func TestStrutil_RemoveDuplicate(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		require.Equal(t, []string{}, RemoveDuplicate([]string{}))
	})
	t.Run("all unique preserves order", func(t *testing.T) {
		in := []string{"a", "b", "c"}
		require.Equal(t, []string{"a", "b", "c"}, RemoveDuplicate(in))
	})
	t.Run("removes duplicates keeps first occurrence order", func(t *testing.T) {
		in := []string{"b", "a", "b", "c", "a", "c"}
		require.Equal(t, []string{"b", "a", "c"}, RemoveDuplicate(in))
	})
	t.Run("empty-string element is a distinct value", func(t *testing.T) {
		in := []string{"", "", "x"}
		require.Equal(t, []string{"", "x"}, RemoveDuplicate(in))
	})
}

func TestStrutil_acKey(t *testing.T) {
	t.Run("empty dict returns empty key", func(t *testing.T) {
		assert.Equal(t, "", acKey(nil))
		assert.Equal(t, "", acKey([]string{}))
	})
	t.Run("whitespace-only entries collapse to empty key", func(t *testing.T) {
		assert.Equal(t, "", acKey([]string{"  ", "\t", ""}))
	})
	t.Run("order independent", func(t *testing.T) {
		k1 := acKey([]string{"alpha", "beta", "gamma"})
		k2 := acKey([]string{"gamma", "alpha", "beta"})
		require.NotEmpty(t, k1)
		assert.Equal(t, k1, k2)
	})
	t.Run("case and surrounding whitespace normalized", func(t *testing.T) {
		k1 := acKey([]string{"Foo", " BAR "})
		k2 := acKey([]string{"foo", "bar"})
		assert.Equal(t, k1, k2)
	})
	t.Run("different content yields different key", func(t *testing.T) {
		k1 := acKey([]string{"foo"})
		k2 := acKey([]string{"bar"})
		assert.NotEqual(t, k1, k2)
	})
}

func TestStrutil_AcSearch(t *testing.T) {
	t.Run("empty dict returns false", func(t *testing.T) {
		ok, words := AcSearch("hello", []string{}, true)
		assert.False(t, ok)
		assert.Nil(t, words)
	})
	t.Run("empty text returns false", func(t *testing.T) {
		ok, words := AcSearch("", []string{"hello"}, true)
		assert.False(t, ok)
		assert.Nil(t, words)
	})
	t.Run("match found returns hit words", func(t *testing.T) {
		ok, words := AcSearch("say hello there", []string{"hello", "world"}, true)
		assert.True(t, ok)
		assert.Contains(t, words, "hello")
	})
	t.Run("no match returns false", func(t *testing.T) {
		ok, words := AcSearch("nothing here", []string{"zzz", "qqq"}, true)
		assert.False(t, ok)
		assert.Nil(t, words)
	})
	t.Run("dict of only blank words yields nil machine and false", func(t *testing.T) {
		ok, words := AcSearch("anything", []string{"   ", ""}, true)
		assert.False(t, ok)
		assert.Nil(t, words)
	})
}

// ---------------------------------------------------------------------------
// sensitive.go
// ---------------------------------------------------------------------------

func strutilWithSensitiveWords(t *testing.T, words []string) {
	t.Helper()
	orig := setting.SensitiveWords
	setting.SensitiveWords = words
	t.Cleanup(func() { setting.SensitiveWords = orig })
}

func TestStrutil_SensitiveWordContains(t *testing.T) {
	t.Run("no configured words returns false", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{})
		ok, words := SensitiveWordContains("anything at all")
		assert.False(t, ok)
		assert.Nil(t, words)
	})
	t.Run("empty text returns false", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"bad"})
		ok, words := SensitiveWordContains("")
		assert.False(t, ok)
		assert.Nil(t, words)
	})
	t.Run("case-insensitive match", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"badword"})
		ok, words := SensitiveWordContains("This has a BadWord inside")
		assert.True(t, ok)
		assert.Contains(t, words, "badword")
	})
	t.Run("clean text returns false", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"badword"})
		ok, words := SensitiveWordContains("perfectly clean text")
		assert.False(t, ok)
		assert.Nil(t, words)
	})
}

func TestStrutil_CheckSensitiveText(t *testing.T) {
	strutilWithSensitiveWords(t, []string{"secret"})
	ok, words := CheckSensitiveText("the secret is out")
	assert.True(t, ok)
	assert.Contains(t, words, "secret")

	ok2, words2 := CheckSensitiveText("nothing to see")
	assert.False(t, ok2)
	assert.Nil(t, words2)
}

func TestStrutil_SensitiveWordReplace(t *testing.T) {
	t.Run("no configured words returns original text unchanged", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{})
		ok, words, out := SensitiveWordReplace("hello badword", true)
		assert.False(t, ok)
		assert.Nil(t, words)
		assert.Equal(t, "hello badword", out)
	})
	t.Run("replaces detected word with mask", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"badword"})
		ok, words, out := SensitiveWordReplace("a badword here", false)
		assert.True(t, ok)
		assert.Contains(t, words, "badword")
		assert.Contains(t, out, "**###**")
		assert.NotContains(t, out, "badword")
	})
	t.Run("clean text left intact", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"badword"})
		ok, words, out := SensitiveWordReplace("totally fine", false)
		assert.False(t, ok)
		assert.Nil(t, words)
		assert.Equal(t, "totally fine", out)
	})
}

func TestStrutil_CheckSensitiveMessages(t *testing.T) {
	t.Run("empty messages returns nil", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"bad"})
		words, err := CheckSensitiveMessages(nil)
		require.NoError(t, err)
		assert.Nil(t, words)
	})
	t.Run("clean message returns no error", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"bad"})
		msgs := []dto.Message{{Role: "user", Content: "hello there"}}
		words, err := CheckSensitiveMessages(msgs)
		require.NoError(t, err)
		assert.Nil(t, words)
	})
	t.Run("sensitive text message returns error and words", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"forbidden"})
		msgs := []dto.Message{{Role: "user", Content: "this is Forbidden content"}}
		words, err := CheckSensitiveMessages(msgs)
		require.Error(t, err)
		assert.Contains(t, words, "forbidden")
	})
	t.Run("empty content string is skipped", func(t *testing.T) {
		strutilWithSensitiveWords(t, []string{"forbidden"})
		msgs := []dto.Message{{Role: "user", Content: ""}}
		words, err := CheckSensitiveMessages(msgs)
		require.NoError(t, err)
		assert.Nil(t, words)
	})
}

// ---------------------------------------------------------------------------
// group.go
// ---------------------------------------------------------------------------

func TestStrutil_GetUserUsableGroups(t *testing.T) {
	t.Run("empty user group returns base copy only", func(t *testing.T) {
		got := GetUserUsableGroups("")
		assert.Contains(t, got, "default")
		assert.Contains(t, got, "vip")
	})
	t.Run("unknown user group added with default description", func(t *testing.T) {
		got := GetUserUsableGroups("brandnew")
		assert.Equal(t, "用户分组", got["brandnew"])
		// base groups preserved
		assert.Contains(t, got, "default")
	})
	t.Run("known base group not overwritten with default desc", func(t *testing.T) {
		got := GetUserUsableGroups("default")
		assert.Equal(t, "默认分组", got["default"])
	})
	t.Run("special settings add/remove/direct branches", func(t *testing.T) {
		const ug = "strutil_special_ug"
		gr := ratio_setting.GetGroupRatioSetting()
		gr.GroupSpecialUsableGroup.Set(ug, map[string]string{
			"-:vip":      "remove vip",
			"+:extra":    "add extra",
			"plainadded": "direct add",
		})
		t.Cleanup(func() { gr.GroupSpecialUsableGroup.Set(ug, map[string]string{}) })

		got := GetUserUsableGroups(ug)
		// removed
		assert.NotContains(t, got, "vip")
		// +: prefixed added under trimmed name
		assert.Equal(t, "add extra", got["extra"])
		// direct add
		assert.Equal(t, "direct add", got["plainadded"])
		// the user group itself present (added by fallback since not in base map)
		assert.Contains(t, got, ug)
	})
}

func TestStrutil_GroupInUserUsableGroups(t *testing.T) {
	assert.True(t, GroupInUserUsableGroups("", "default"))
	assert.False(t, GroupInUserUsableGroups("", "does-not-exist"))
	// unknown user group is injected into its own usable set
	assert.True(t, GroupInUserUsableGroups("selfgroup", "selfgroup"))
}

func TestStrutil_GetUserAutoGroup(t *testing.T) {
	// default auto groups = {"default"}, which is present in base usable groups.
	got := GetUserAutoGroup("")
	assert.Equal(t, []string{"default"}, got)

	// Non-empty return is always a subset of usable groups.
	got2 := GetUserAutoGroup("vip")
	for _, g := range got2 {
		assert.Contains(t, GetUserUsableGroups("vip"), g)
	}
}

// ---------------------------------------------------------------------------
// usage_helpr.go
// ---------------------------------------------------------------------------

func strutilNewTestCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	return c
}

func TestStrutil_ResponseText2Usage(t *testing.T) {
	t.Run("empty response text yields zero completion tokens", func(t *testing.T) {
		c := strutilNewTestCtx()
		usage := ResponseText2Usage(c, "", "gpt-4", 10)
		require.NotNil(t, usage)
		assert.Equal(t, 10, usage.PromptTokens)
		assert.Equal(t, 0, usage.CompletionTokens)
		assert.Equal(t, 10, usage.TotalTokens)
		// context flag set to signal local token counting
		v, ok := common.GetContextKey(c, constant.ContextKeyLocalCountTokens)
		assert.True(t, ok)
		assert.Equal(t, true, v)
	})
	t.Run("non-empty response text adds completion tokens", func(t *testing.T) {
		c := strutilNewTestCtx()
		usage := ResponseText2Usage(c, "some completion output text", "gpt-4", 5)
		require.NotNil(t, usage)
		assert.Equal(t, 5, usage.PromptTokens)
		assert.Greater(t, usage.CompletionTokens, 0)
		assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
	})
}

func TestStrutil_ValidUsage(t *testing.T) {
	assert.False(t, ValidUsage(nil))
	assert.False(t, ValidUsage(&dto.Usage{}))
	assert.True(t, ValidUsage(&dto.Usage{PromptTokens: 1}))
	assert.True(t, ValidUsage(&dto.Usage{CompletionTokens: 1}))
	assert.True(t, ValidUsage(&dto.Usage{PromptTokens: 3, CompletionTokens: 4}))
}

// ---------------------------------------------------------------------------
// return_path.go
// ---------------------------------------------------------------------------

func TestStrutil_PaymentReturnURL(t *testing.T) {
	origAddr := system_setting.ServerAddress
	origTheme := common.GetTheme()
	t.Cleanup(func() {
		system_setting.ServerAddress = origAddr
		common.SetTheme(origTheme)
	})

	t.Run("classic theme leaves console path unchanged and trims trailing slash", func(t *testing.T) {
		common.SetTheme("classic")
		system_setting.ServerAddress = "https://example.com/"
		got := PaymentReturnURL("/console/topup")
		assert.Equal(t, "https://example.com/console/topup", got)
	})
	t.Run("default theme rewrites console/topup to wallet", func(t *testing.T) {
		common.SetTheme("default")
		system_setting.ServerAddress = "https://example.com"
		got := PaymentReturnURL("/console/topup")
		assert.Equal(t, "https://example.com/wallet", got)
	})
}

// ---------------------------------------------------------------------------
// openai_chat_responses_mode.go
// ---------------------------------------------------------------------------

func TestStrutil_ShouldChatCompletionsUseResponsesPolicy(t *testing.T) {
	base := model_setting.ChatCompletionsToResponsesPolicy{
		Enabled:       true,
		AllChannels:   true,
		ModelPatterns: []string{"^gpt-4"},
	}

	t.Run("disabled policy always false", func(t *testing.T) {
		p := base
		p.Enabled = false
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 1, 1, "gpt-4o"))
	})
	t.Run("enabled all-channels with matching model", func(t *testing.T) {
		assert.True(t, ShouldChatCompletionsUseResponsesPolicy(base, 1, 1, "gpt-4o"))
	})
	t.Run("enabled all-channels but model does not match", func(t *testing.T) {
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(base, 1, 1, "claude-3"))
	})
	t.Run("channel not enabled short-circuits before model match", func(t *testing.T) {
		p := model_setting.ChatCompletionsToResponsesPolicy{
			Enabled:       true,
			AllChannels:   false,
			ChannelIDs:    []int{99},
			ModelPatterns: []string{"^gpt-4"},
		}
		// channelID 5 not in list, channelType 0 not matched -> disabled
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 5, 0, "gpt-4o"))
		// channelID 99 in list -> enabled and model matches
		assert.True(t, ShouldChatCompletionsUseResponsesPolicy(p, 99, 0, "gpt-4o"))
	})
	t.Run("empty model patterns never match", func(t *testing.T) {
		p := base
		p.ModelPatterns = nil
		assert.False(t, ShouldChatCompletionsUseResponsesPolicy(p, 1, 1, "gpt-4o"))
	})
}

func TestStrutil_ShouldChatCompletionsUseResponsesGlobal(t *testing.T) {
	gs := model_setting.GetGlobalSettings()
	orig := gs.ChatCompletionsToResponsesPolicy
	t.Cleanup(func() { gs.ChatCompletionsToResponsesPolicy = orig })

	// default global policy is disabled
	gs.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{Enabled: false}
	assert.False(t, ShouldChatCompletionsUseResponsesGlobal(1, 1, "gpt-4o"))

	// enable and match
	gs.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{
		Enabled:       true,
		AllChannels:   true,
		ModelPatterns: []string{"^gpt-4"},
	}
	assert.True(t, ShouldChatCompletionsUseResponsesGlobal(1, 1, "gpt-4o"))
	assert.False(t, ShouldChatCompletionsUseResponsesGlobal(1, 1, "gemini-pro"))
}

// ---------------------------------------------------------------------------
// openai_chat_responses_compat.go
// ---------------------------------------------------------------------------

func TestStrutil_ChatCompletionsRequestToResponsesRequest(t *testing.T) {
	t.Run("nil request errors", func(t *testing.T) {
		_, err := ChatCompletionsRequestToResponsesRequest(nil)
		require.Error(t, err)
	})
	t.Run("missing model errors", func(t *testing.T) {
		_, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{})
		require.Error(t, err)
	})
	t.Run("n greater than 1 unsupported", func(t *testing.T) {
		n := 2
		_, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
			Model: "gpt-4o",
			N:     &n,
		})
		require.Error(t, err)
	})
	t.Run("valid request converts and preserves model", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model: "gpt-4o",
			Messages: []dto.Message{
				{Role: "user", Content: "hello"},
			},
		}
		out, err := ChatCompletionsRequestToResponsesRequest(req)
		require.NoError(t, err)
		require.NotNil(t, out)
		assert.Equal(t, "gpt-4o", out.Model)
	})
}

func TestStrutil_ResponsesFinishReasonFromStatus(t *testing.T) {
	t.Run("nil response returns false", func(t *testing.T) {
		reason, ok := ResponsesFinishReasonFromStatus(nil)
		assert.False(t, ok)
		assert.Equal(t, "", reason)
	})
	t.Run("non-incomplete status returns false", func(t *testing.T) {
		resp := &dto.OpenAIResponsesResponse{Status: []byte(`"completed"`)}
		reason, ok := ResponsesFinishReasonFromStatus(resp)
		assert.False(t, ok)
		assert.Equal(t, "", reason)
	})
	t.Run("incomplete content_filter maps to content_filter", func(t *testing.T) {
		resp := &dto.OpenAIResponsesResponse{
			Status:            []byte(`"incomplete"`),
			IncompleteDetails: &dto.IncompleteDetails{Reason: "content_filter"},
		}
		reason, ok := ResponsesFinishReasonFromStatus(resp)
		assert.True(t, ok)
		assert.Equal(t, "content_filter", reason)
	})
	t.Run("incomplete other reason maps to length", func(t *testing.T) {
		resp := &dto.OpenAIResponsesResponse{
			Status:            []byte(`"incomplete"`),
			IncompleteDetails: &dto.IncompleteDetails{Reason: "max_output_tokens"},
		}
		reason, ok := ResponsesFinishReasonFromStatus(resp)
		assert.True(t, ok)
		assert.Equal(t, "length", reason)
	})
	t.Run("incomplete with nil details maps to length", func(t *testing.T) {
		resp := &dto.OpenAIResponsesResponse{Status: []byte(`"incomplete"`)}
		reason, ok := ResponsesFinishReasonFromStatus(resp)
		assert.True(t, ok)
		assert.Equal(t, "length", reason)
	})
}

func TestStrutil_ExtractOutputTextFromResponses(t *testing.T) {
	t.Run("nil response empty", func(t *testing.T) {
		assert.Equal(t, "", ExtractOutputTextFromResponses(nil))
	})
	t.Run("no output empty", func(t *testing.T) {
		assert.Equal(t, "", ExtractOutputTextFromResponses(&dto.OpenAIResponsesResponse{}))
	})
	t.Run("assistant message output_text concatenated", func(t *testing.T) {
		resp := &dto.OpenAIResponsesResponse{
			Output: []dto.ResponsesOutput{
				{
					Type: "message",
					Role: "assistant",
					Content: []dto.ResponsesOutputContent{
						{Type: "output_text", Text: "Hello "},
						{Type: "output_text", Text: "World"},
					},
				},
			},
		}
		assert.Equal(t, "Hello World", ExtractOutputTextFromResponses(resp))
	})
	t.Run("non-assistant role message skipped in preferred pass but caught by fallback", func(t *testing.T) {
		resp := &dto.OpenAIResponsesResponse{
			Output: []dto.ResponsesOutput{
				{
					Type: "message",
					Role: "user",
					Content: []dto.ResponsesOutputContent{
						{Type: "output_text", Text: "fallback text"},
					},
				},
			},
		}
		// preferred pass skips (role != assistant); fallback collects any text.
		assert.Equal(t, "fallback text", ExtractOutputTextFromResponses(resp))
	})
}

// ---------------------------------------------------------------------------
// convert.go
// ---------------------------------------------------------------------------

func TestStrutil_NormalizeCacheCreationSplit(t *testing.T) {
	cases := []struct {
		name          string
		total         int
		t5m           int
		t1h           int
		want5m        int
		want1h        int
	}{
		{"remainder folded into 5m bucket", 100, 30, 20, 80, 20},
		{"exact split no remainder", 50, 30, 20, 30, 20},
		{"negative remainder clamped to zero", 10, 30, 20, 30, 20},
		{"all zero", 0, 0, 0, 0, 0},
		{"only total goes to 5m", 40, 0, 0, 40, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got5m, got1h := NormalizeCacheCreationSplit(tc.total, tc.t5m, tc.t1h)
			assert.Equal(t, tc.want5m, got5m)
			assert.Equal(t, tc.want1h, got1h)
		})
	}
}

func TestStrutil_ResponseOpenAI2Claude(t *testing.T) {
	t.Run("text choice becomes claude text content", func(t *testing.T) {
		oaiResp := &dto.OpenAITextResponse{
			Id:    "resp-1",
			Model: "gpt-4o",
			Choices: []dto.OpenAITextResponseChoice{
				{
					Index:        0,
					Message:      dto.Message{Role: "assistant", Content: "hi there"},
					FinishReason: "stop",
				},
			},
			Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
		}
		got := ResponseOpenAI2Claude(oaiResp, nil)
		require.NotNil(t, got)
		assert.Equal(t, "resp-1", got.Id)
		assert.Equal(t, "assistant", got.Role)
		assert.Equal(t, "message", got.Type)
		require.NotEmpty(t, got.Content)
		assert.Equal(t, "text", got.Content[0].Type)
		assert.Equal(t, "hi there", got.Content[0].GetText())
	})
	t.Run("empty choices yields empty content", func(t *testing.T) {
		oaiResp := &dto.OpenAITextResponse{Id: "resp-2", Model: "gpt-4o"}
		got := ResponseOpenAI2Claude(oaiResp, nil)
		require.NotNil(t, got)
		assert.Equal(t, "resp-2", got.Id)
		assert.Empty(t, got.Content)
	})
}
