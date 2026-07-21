package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ChannelOtherSettings.IsOpenRouterEnterprise
// ---------------------------------------------------------------------------

func TestIsOpenRouterEnterprise(t *testing.T) {
	var nilS *ChannelOtherSettings
	assert.False(t, nilS.IsOpenRouterEnterprise())
	assert.False(t, (&ChannelOtherSettings{}).IsOpenRouterEnterprise(), "nil pointer field => false")
	tr := true
	fa := false
	assert.True(t, (&ChannelOtherSettings{OpenRouterEnterprise: &tr}).IsOpenRouterEnterprise())
	assert.False(t, (&ChannelOtherSettings{OpenRouterEnterprise: &fa}).IsOpenRouterEnterprise())
}

// ---------------------------------------------------------------------------
// AdvancedCustomConfig path matching
// ---------------------------------------------------------------------------

func TestAdvancedCustomConfig_MatchPath(t *testing.T) {
	var nilC *AdvancedCustomConfig
	_, ok := nilC.MatchPath("/x")
	assert.False(t, ok)

	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions"},
		{IncomingPath: "/v1beta/models/{model}:generateContent"},
	}}
	// exact match
	r, ok := c.MatchPath("/v1/chat/completions")
	require.True(t, ok)
	assert.Equal(t, "/v1/chat/completions", r.IncomingPath)
	// {model} placeholder match
	_, ok = c.MatchPath("/v1beta/models/gemini-pro:generateContent")
	assert.True(t, ok)
	// :generateContent <-> :streamGenerateContent equivalence
	_, ok = c.MatchPath("/v1beta/models/gemini-pro:streamGenerateContent")
	assert.True(t, ok)
	// no match
	_, ok = c.MatchPath("/v1/embeddings")
	assert.False(t, ok)
	// placeholder must not contain slash
	_, ok = c.MatchPath("/v1beta/models/a/b:generateContent")
	assert.False(t, ok)
	// placeholder must be non-empty
	_, ok = c.MatchPath("/v1beta/models/:generateContent")
	assert.False(t, ok)
}

func TestAdvancedCustomConfig_SupportsPath(t *testing.T) {
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{{IncomingPath: "/v1/rerank"}}}
	assert.True(t, c.SupportsPath("/v1/rerank"))
	assert.False(t, c.SupportsPath("/v1/nope"))
}

func TestAdvancedCustomConfig_MatchPathForModel(t *testing.T) {
	var nilC *AdvancedCustomConfig
	_, ok := nilC.MatchPathForModel("/x", "m")
	assert.False(t, ok)

	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", Models: []string{"gpt-4"}},
		{IncomingPath: "/v1/chat/completions", Models: []string{"re:^claude-.*$"}},
		{IncomingPath: "/v1/chat/completions"}, // catch-all
	}}
	// exact model
	r, ok := c.MatchPathForModel("/v1/chat/completions", "gpt-4")
	require.True(t, ok)
	assert.Equal(t, []string{"gpt-4"}, r.Models)
	// regex model
	r, ok = c.MatchPathForModel("/v1/chat/completions", "claude-3")
	require.True(t, ok)
	assert.Equal(t, []string{"re:^claude-.*$"}, r.Models)
	// falls to catch-all
	r, ok = c.MatchPathForModel("/v1/chat/completions", "mistral")
	require.True(t, ok)
	assert.Empty(t, r.Models)

	assert.True(t, c.SupportsPathForModel("/v1/chat/completions", "gpt-4"))
	assert.False(t, c.SupportsPathForModel("/v1/other", "gpt-4"))
}

func TestAdvancedCustomConfig_ModelRegexMatch(t *testing.T) {
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", Models: []string{"re:[invalid("}}, // invalid regex, cached nil, never matches
	}}
	_, ok := c.MatchPathForModel("/v1/chat/completions", "anything")
	assert.False(t, ok)

	// empty regex pattern never matches
	c2 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", Models: []string{"re:"}},
	}}
	_, ok = c2.MatchPathForModel("/v1/chat/completions", "x")
	assert.False(t, ok)
}

func TestAdvancedCustomConfig_ModelListRoute(t *testing.T) {
	var nilC *AdvancedCustomConfig
	_, ok := nilC.ModelListRoute()
	assert.False(t, ok)

	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions"},
		{IncomingPath: AdvancedCustomModelListPath},
	}}
	r, ok := c.ModelListRoute()
	require.True(t, ok)
	assert.Equal(t, AdvancedCustomModelListPath, r.IncomingPath)

	// no model-list route
	c2 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{{IncomingPath: "/v1/chat/completions"}}}
	_, ok = c2.ModelListRoute()
	assert.False(t, ok)
}

func TestAdvancedCustomConfig_SupportedEndpointTypesForModel(t *testing.T) {
	var nilC *AdvancedCustomConfig
	assert.Nil(t, nilC.SupportedEndpointTypesForModel("m"))

	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions"},
		{IncomingPath: "/v1/responses"},
		{IncomingPath: "/v1/responses/compact"},
		{IncomingPath: "/v1/messages"},
		{IncomingPath: "/v1/rerank"},
		{IncomingPath: "/v1/images/generations"},
		{IncomingPath: "/v1/embeddings"},
		{IncomingPath: "/v1beta/models/{model}:generateContent"},
		{IncomingPath: "/unknown/path"}, // not mapped, skipped
		{IncomingPath: "/v1/chat/completions"}, // duplicate endpoint type, dedup
	}}
	endpoints := c.SupportedEndpointTypesForModel("any-model")
	assert.Contains(t, endpoints, constant.EndpointTypeOpenAI)
	assert.Contains(t, endpoints, constant.EndpointTypeOpenAIResponse)
	assert.Contains(t, endpoints, constant.EndpointTypeOpenAIResponseCompact)
	assert.Contains(t, endpoints, constant.EndpointTypeAnthropic)
	assert.Contains(t, endpoints, constant.EndpointTypeJinaRerank)
	assert.Contains(t, endpoints, constant.EndpointTypeImageGeneration)
	assert.Contains(t, endpoints, constant.EndpointTypeEmbeddings)
	assert.Contains(t, endpoints, constant.EndpointTypeGemini)
	// dedup: OpenAI only appears once
	count := 0
	for _, e := range endpoints {
		if e == constant.EndpointTypeOpenAI {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestAdvancedCustomConfig_SupportedEndpointTypes_ModelFilter(t *testing.T) {
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", Models: []string{"gpt-4"}},
	}}
	assert.Empty(t, c.SupportedEndpointTypesForModel("other"))
	assert.NotEmpty(t, c.SupportedEndpointTypesForModel("gpt-4"))
}

// ---------------------------------------------------------------------------
// IsAdvancedCustomConverterAllowed
// ---------------------------------------------------------------------------

func TestIsAdvancedCustomConverterAllowed(t *testing.T) {
	allowed := []string{
		"none",
		"anthropic_messages_to_openai_chat_completions",
		"openai_chat_completions_to_anthropic_messages",
		"openai_chat_completions_to_openai_responses",
		"openai_responses_to_openai_chat_completions",
		"openai_responses_to_gemini_generate_content",
		"gemini_generate_content_to_openai_chat_completions",
		"openai_chat_completions_to_gemini_generate_content",
	}
	for _, conv := range allowed {
		assert.Truef(t, IsAdvancedCustomConverterAllowed(conv), "converter %q", conv)
	}
	assert.False(t, IsAdvancedCustomConverterAllowed("bogus"))
	assert.False(t, IsAdvancedCustomConverterAllowed(""))
}

// ---------------------------------------------------------------------------
// AdvancedCustomConfig.Validate
// ---------------------------------------------------------------------------

func TestAdvancedCustomConfig_Validate_NilAndEmpty(t *testing.T) {
	var nilC *AdvancedCustomConfig
	assert.Error(t, nilC.Validate())
	assert.Error(t, (&AdvancedCustomConfig{}).Validate(), "no routes => error")
}

func TestAdvancedCustomConfig_Validate_Valid(t *testing.T) {
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/chat/completions", Converter: "none"},
		{IncomingPath: "/v1/messages", UpstreamPath: "https://api.host/v1/chat/completions", Converter: "anthropic_messages_to_openai_chat_completions"},
	}}
	assert.NoError(t, c.Validate())
}

func TestAdvancedCustomConfig_Validate_IncomingPathErrors(t *testing.T) {
	cases := []struct {
		name  string
		route AdvancedCustomRoute
	}{
		{"empty incoming", AdvancedCustomRoute{UpstreamPath: "/x"}},
		{"no leading slash", AdvancedCustomRoute{IncomingPath: "v1/chat", UpstreamPath: "/x"}},
		{"has query", AdvancedCustomRoute{IncomingPath: "/v1/chat?a=b", UpstreamPath: "/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{tc.route}}
			assert.Error(t, c.Validate())
		})
	}
}

func TestAdvancedCustomConfig_Validate_UpstreamErrors(t *testing.T) {
	cases := []struct {
		name  string
		route AdvancedCustomRoute
	}{
		{"missing upstream", AdvancedCustomRoute{IncomingPath: "/v1/chat/completions", Converter: "none"}},
		{"double slash", AdvancedCustomRoute{IncomingPath: "/v1/chat/completions", UpstreamPath: "//evil", Converter: "none"}},
		{"non-http scheme", AdvancedCustomRoute{IncomingPath: "/v1/chat/completions", UpstreamPath: "ftp://host/x", Converter: "none"}},
		{"not url not path", AdvancedCustomRoute{IncomingPath: "/v1/chat/completions", UpstreamPath: "just-text", Converter: "none"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{tc.route}}
			assert.Error(t, c.Validate())
		})
	}
}

func TestAdvancedCustomConfig_Validate_ConverterMismatch(t *testing.T) {
	// converter not registered
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "nonexistent"},
	}}
	assert.Error(t, c.Validate())

	// converter requires a specific incoming path
	c2 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/embeddings", UpstreamPath: "/x", Converter: "anthropic_messages_to_openai_chat_completions"},
	}}
	assert.Error(t, c2.Validate())
}

func TestAdvancedCustomConfig_Validate_ConverterPathMatrix(t *testing.T) {
	valid := []AdvancedCustomRoute{
		{IncomingPath: "/v1/messages", UpstreamPath: "/x", Converter: "anthropic_messages_to_openai_chat_completions"},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "openai_chat_completions_to_anthropic_messages"},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "openai_chat_completions_to_openai_responses"},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "openai_chat_completions_to_gemini_generate_content"},
		{IncomingPath: "/v1/responses", UpstreamPath: "/x", Converter: "openai_responses_to_openai_chat_completions"},
		{IncomingPath: "/v1/responses", UpstreamPath: "/x", Converter: "openai_responses_to_gemini_generate_content"},
		{IncomingPath: "/v1beta/models/{model}:generateContent", UpstreamPath: "/x", Converter: "gemini_generate_content_to_openai_chat_completions"},
	}
	for _, r := range valid {
		c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{r}}
		assert.NoErrorf(t, c.Validate(), "route %s / %s", r.IncomingPath, r.Converter)
	}
}

func TestAdvancedCustomConfig_Validate_ModelDuplicatesAndCatchAll(t *testing.T) {
	// duplicate model in same route
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none", Models: []string{"gpt-4", "gpt-4"}},
	}}
	assert.Error(t, c.Validate())

	// model overlaps across routes with same incoming path
	c2 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none", Models: []string{"gpt-4"}},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/y", Converter: "none", Models: []string{"gpt-4"}},
	}}
	assert.Error(t, c2.Validate())

	// catch-all must be last: catch-all before model-specific
	c3 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none"}, // catch-all
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/y", Converter: "none", Models: []string{"gpt-4"}},
	}}
	assert.Error(t, c3.Validate())

	// two catch-alls for same path
	c4 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none"},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/y", Converter: "none"},
	}}
	assert.Error(t, c4.Validate())

	// valid: model-specific then catch-all last
	c5 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none", Models: []string{"gpt-4"}},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/y", Converter: "none"},
	}}
	assert.NoError(t, c5.Validate())
}

func TestAdvancedCustomConfig_Validate_RegexModelRule(t *testing.T) {
	// invalid regex
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none", Models: []string{"re:[bad("}},
	}}
	assert.Error(t, c.Validate())
	// empty regex
	c2 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none", Models: []string{"re:"}},
	}}
	assert.Error(t, c2.Validate())
	// valid regex
	c3 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none", Models: []string{"re:^gpt-.*$"}},
	}}
	assert.NoError(t, c3.Validate())
}

func TestAdvancedCustomConfig_Validate_ModelListRoute(t *testing.T) {
	// valid model list route
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/models", UpstreamPath: "/v1/models", Converter: "none"},
	}}
	assert.NoError(t, c.Validate())

	// model list route with models => error
	c2 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/models", UpstreamPath: "/v1/models", Converter: "none", Models: []string{"gpt-4"}},
	}}
	assert.Error(t, c2.Validate())

	// model list route with non-none converter => error
	c3 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/models", UpstreamPath: "/v1/models", Converter: "openai_chat_completions_to_openai_responses"},
	}}
	assert.Error(t, c3.Validate())

	// model list route upstream with {model} placeholder => error
	c4 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/models", UpstreamPath: "/v1/models/{model}", Converter: "none"},
	}}
	assert.Error(t, c4.Validate())

	// duplicate model list routes => error
	c5 := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/models", UpstreamPath: "/a", Converter: "none"},
		{IncomingPath: "/v1/models", UpstreamPath: "/b", Converter: "none"},
	}}
	assert.Error(t, c5.Validate())
}

func TestAdvancedCustomConfig_Validate_Auth(t *testing.T) {
	base := func(auth *AdvancedCustomRouteAuth) *AdvancedCustomConfig {
		return &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
			{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x", Converter: "none", Auth: auth},
		}}
	}
	// nil auth is fine
	assert.NoError(t, base(nil).Validate())
	// none
	assert.NoError(t, base(&AdvancedCustomRouteAuth{Type: "none"}).Validate())
	// header valid
	assert.NoError(t, base(&AdvancedCustomRouteAuth{Type: "header", Name: "X-Key", Value: "v"}).Validate())
	// query valid
	assert.NoError(t, base(&AdvancedCustomRouteAuth{Type: "query", Name: "key", Value: "v"}).Validate())
	// header missing name
	assert.Error(t, base(&AdvancedCustomRouteAuth{Type: "header", Value: "v"}).Validate())
	// header missing value
	assert.Error(t, base(&AdvancedCustomRouteAuth{Type: "header", Name: "X"}).Validate())
	// invalid type
	assert.Error(t, base(&AdvancedCustomRouteAuth{Type: "basic"}).Validate())
}

func TestAdvancedCustomConfig_Validate_DefaultConverterNone(t *testing.T) {
	// empty converter defaults to "none" and validates
	c := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/x"},
	}}
	assert.NoError(t, c.Validate())
}
