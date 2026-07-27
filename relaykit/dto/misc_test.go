package dto

import (
	"encoding/json"
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// error.go — GeneralErrorResponse
// ---------------------------------------------------------------------------

func TestGeneralErrorResponse_TryToOpenAIError(t *testing.T) {
	// object error with message
	e := GeneralErrorResponse{Error: json.RawMessage(`{"message":"bad request (request id: abc)","type":"invalid"}`)}
	oe := e.TryToOpenAIError()
	require.NotNil(t, oe)
	assert.Equal(t, "bad request", oe.Message, "request id stripped")
	assert.Equal(t, "invalid", oe.Type)

	// empty error => nil
	assert.Nil(t, GeneralErrorResponse{}.TryToOpenAIError())
	// error present but empty message => nil
	assert.Nil(t, GeneralErrorResponse{Error: json.RawMessage(`{"type":"x"}`)}.TryToOpenAIError())
}

func TestGeneralErrorResponse_ToMessage(t *testing.T) {
	// object error
	assert.Equal(t, "boom", GeneralErrorResponse{Error: json.RawMessage(`{"message":"boom"}`)}.ToMessage())
	// string error
	assert.Equal(t, "str-err", GeneralErrorResponse{Error: json.RawMessage(`"str-err"`)}.ToMessage())
	// non-object/string (number) => raw text
	assert.Equal(t, "42", GeneralErrorResponse{Error: json.RawMessage(`42`)}.ToMessage())
	// object without message => falls through to Message field
	assert.Equal(t, "msgField", GeneralErrorResponse{Error: json.RawMessage(`{"foo":1}`), Message: "msgField"}.ToMessage())

	// fallback ladder: Message > Msg > Err > ErrorMsg > Detail > Header.Message > Response.Error.Message
	assert.Equal(t, "m", GeneralErrorResponse{Message: "m"}.ToMessage())
	assert.Equal(t, "msg", GeneralErrorResponse{Msg: "msg"}.ToMessage())
	assert.Equal(t, "err", GeneralErrorResponse{Err: "err"}.ToMessage())
	assert.Equal(t, "emsg", GeneralErrorResponse{ErrorMsg: "emsg"}.ToMessage())
	assert.Equal(t, "detail", GeneralErrorResponse{Detail: "detail"}.ToMessage())
	hdr := GeneralErrorResponse{}
	hdr.Header.Message = "hm"
	assert.Equal(t, "hm", hdr.ToMessage())
	resp := GeneralErrorResponse{}
	resp.Response.Error.Message = "rm"
	assert.Equal(t, "rm", resp.ToMessage())
	// nothing set => empty
	assert.Equal(t, "", GeneralErrorResponse{}.ToMessage())
}

// ---------------------------------------------------------------------------
// values.go — StringValue / IntValue / BoolValue
// ---------------------------------------------------------------------------

func TestStringValue(t *testing.T) {
	// from string
	var s StringValue
	require.NoError(t, json.Unmarshal([]byte(`"hi"`), &s))
	assert.Equal(t, StringValue("hi"), s)
	// from number => string form
	require.NoError(t, json.Unmarshal([]byte(`123`), &s))
	assert.Equal(t, StringValue("123"), s)
	require.NoError(t, json.Unmarshal([]byte(`1.5`), &s))
	assert.Equal(t, StringValue("1.5"), s)
	// marshal back to string
	out, err := json.Marshal(StringValue("x"))
	require.NoError(t, err)
	assert.JSONEq(t, `"x"`, string(out))
	// invalid (object) => error
	assert.Error(t, json.Unmarshal([]byte(`{}`), &s))
}

func TestIntValue(t *testing.T) {
	var i IntValue
	require.NoError(t, json.Unmarshal([]byte(`5`), &i))
	assert.Equal(t, IntValue(5), i)
	// from numeric string
	require.NoError(t, json.Unmarshal([]byte(`"7"`), &i))
	assert.Equal(t, IntValue(7), i)
	// non-numeric string => error
	assert.Error(t, json.Unmarshal([]byte(`"abc"`), &i))
	// wrong type (object) => error
	assert.Error(t, json.Unmarshal([]byte(`{}`), &i))
	// marshal
	out, err := json.Marshal(IntValue(9))
	require.NoError(t, err)
	assert.JSONEq(t, `9`, string(out))
}

func TestBoolValue(t *testing.T) {
	var b BoolValue
	require.NoError(t, json.Unmarshal([]byte(`true`), &b))
	assert.Equal(t, BoolValue(true), b)
	// from "true"/"false" strings
	require.NoError(t, json.Unmarshal([]byte(`"true"`), &b))
	assert.True(t, bool(b))
	require.NoError(t, json.Unmarshal([]byte(`"false"`), &b))
	assert.False(t, bool(b))
	// other string => falls through to bool unmarshal => error
	assert.Error(t, json.Unmarshal([]byte(`"maybe"`), &b))
	// wrong type (object) => error
	assert.Error(t, json.Unmarshal([]byte(`{}`), &b))
	// marshal
	out, err := json.Marshal(BoolValue(true))
	require.NoError(t, err)
	assert.JSONEq(t, `true`, string(out))
}

// ---------------------------------------------------------------------------
// audio.go
// ---------------------------------------------------------------------------

func TestAudioRequest(t *testing.T) {
	// non-gpt model => text_number token type
	meta := (&AudioRequest{Model: "tts-1", Input: "hello"}).GetTokenCountMeta()
	assert.Equal(t, "hello", meta.CombineText)
	assert.Equal(t, types.TokenTypeTextNumber, meta.TokenType)
	// gpt model => tokenizer
	meta = (&AudioRequest{Model: "gpt-4o-audio", Input: "x"}).GetTokenCountMeta()
	assert.Equal(t, types.TokenTypeTokenizer, meta.TokenType)

	// IsStream: sse
	assert.True(t, (&AudioRequest{StreamFormat: "sse"}).IsStream(nil))
	assert.False(t, (&AudioRequest{StreamFormat: ""}).IsStream(nil))

	r := &AudioRequest{Model: "a"}
	r.SetModelName("")
	assert.Equal(t, "a", r.Model)
	r.SetModelName("b")
	assert.Equal(t, "b", r.Model)
}

func TestAudioRequest_Rule5Speed(t *testing.T) {
	var r AudioRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"model":"m","input":"i","voice":"v","speed":0}`), &r))
	require.NotNil(t, r.Speed)
	assert.Equal(t, 0.0, *r.Speed)
	var r2 AudioRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"model":"m","input":"i","voice":"v"}`), &r2))
	assert.Nil(t, r2.Speed)
}

// ---------------------------------------------------------------------------
// embedding.go
// ---------------------------------------------------------------------------

func TestEmbeddingRequest(t *testing.T) {
	assert.False(t, (&EmbeddingRequest{}).IsStream(nil))

	// ParseInput: nil => empty slice (not nil)
	assert.Equal(t, []string{}, (&EmbeddingRequest{}).ParseInput())
	// string
	assert.Equal(t, []string{"a"}, (&EmbeddingRequest{Input: "a"}).ParseInput())
	// array
	assert.Equal(t, []string{"a", "b"}, (&EmbeddingRequest{Input: []any{"a", "b", 3}}).ParseInput())
	// unsupported type => nil
	assert.Nil(t, (&EmbeddingRequest{Input: 42}).ParseInput())

	meta := (&EmbeddingRequest{Input: []any{"x", "y"}}).GetTokenCountMeta()
	assert.Equal(t, "x\ny", meta.CombineText)

	r := &EmbeddingRequest{Model: "a"}
	r.SetModelName("")
	assert.Equal(t, "a", r.Model)
	r.SetModelName("b")
	assert.Equal(t, "b", r.Model)
}

func TestEmbeddingRequest_Rule5(t *testing.T) {
	var r EmbeddingRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"model":"m","input":"i","dimensions":0,"temperature":0}`), &r))
	require.NotNil(t, r.Dimensions)
	assert.Equal(t, 0, *r.Dimensions)
	require.NotNil(t, r.Temperature)
}

// ---------------------------------------------------------------------------
// rerank.go
// ---------------------------------------------------------------------------

func TestRerankRequest(t *testing.T) {
	assert.False(t, (&RerankRequest{}).IsStream(nil))

	r := &RerankRequest{Query: "q", Documents: []any{"d1", map[string]any{"k": "v"}}}
	meta := r.GetTokenCountMeta()
	assert.Contains(t, meta.CombineText, "d1")
	assert.Contains(t, meta.CombineText, "q")

	// empty query not appended
	meta = (&RerankRequest{Documents: []any{"d"}}).GetTokenCountMeta()
	assert.Equal(t, "d", meta.CombineText)

	r.SetModelName("")
	assert.Equal(t, "", r.Model)
	r.SetModelName("m")
	assert.Equal(t, "m", r.Model)

	// GetReturnDocuments
	assert.False(t, (&RerankRequest{}).GetReturnDocuments())
	tr := true
	assert.True(t, (&RerankRequest{ReturnDocuments: &tr}).GetReturnDocuments())
}

func TestRerankRequest_Rule5(t *testing.T) {
	var r RerankRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{"query":"q","top_n":0,"return_documents":false,"max_chunk_per_doc":0,"overlap_tokens":0}`), &r))
	require.NotNil(t, r.TopN)
	assert.Equal(t, 0, *r.TopN)
	require.NotNil(t, r.ReturnDocuments)
	require.NotNil(t, r.MaxChunkPerDoc)
	require.NotNil(t, r.OverLapTokens)
}

// ---------------------------------------------------------------------------
// openai_responses_compaction_request.go
// ---------------------------------------------------------------------------

func TestOpenAIResponsesCompactionRequest(t *testing.T) {
	r := &OpenAIResponsesCompactionRequest{
		Instructions: json.RawMessage(`"be brief"`),
		Input:        json.RawMessage(`"do it"`),
	}
	meta := r.GetTokenCountMeta()
	assert.Contains(t, meta.CombineText, "be brief")
	assert.Contains(t, meta.CombineText, "do it")
	assert.False(t, r.IsStream(nil))

	r.SetModelName("")
	assert.Equal(t, "", r.Model)
	r.SetModelName("m")
	assert.Equal(t, "m", r.Model)

	// empty parts
	assert.Equal(t, "", (&OpenAIResponsesCompactionRequest{}).GetTokenCountMeta().CombineText)
}

func TestOpenAIResponsesCompactionResponse_GetOpenAIError(t *testing.T) {
	assert.Nil(t, (&OpenAIResponsesCompactionResponse{}).GetOpenAIError())
	assert.NotNil(t, (&OpenAIResponsesCompactionResponse{Error: "x"}).GetOpenAIError())
}

// ---------------------------------------------------------------------------
// openai_video.go
// ---------------------------------------------------------------------------

func TestOpenAIVideo(t *testing.T) {
	v := NewOpenAIVideo()
	assert.Equal(t, "video", v.Object)
	assert.Equal(t, VideoStatusQueued, v.Status)

	v.SetProgressStr("55%")
	assert.Equal(t, 55, v.Progress)
	v.SetProgressStr("80")
	assert.Equal(t, 80, v.Progress)
	// invalid progress => 0 (Atoi error ignored)
	v.SetProgressStr("abc")
	assert.Equal(t, 0, v.Progress)

	// SetMetadata lazily initializes map
	v2 := &OpenAIVideo{}
	v2.SetMetadata("k1", "v1")
	v2.SetMetadata("k2", 2)
	assert.Equal(t, "v1", v2.Metadata["k1"])
	assert.Equal(t, 2, v2.Metadata["k2"])
}

// ---------------------------------------------------------------------------
// notify.go — NewNotify
// ---------------------------------------------------------------------------

func TestNewNotify(t *testing.T) {
	n := NewNotify(NotifyTypeQuotaExceed, "title", "content", []interface{}{1, "x"})
	assert.Equal(t, NotifyTypeQuotaExceed, n.Type)
	assert.Equal(t, "title", n.Title)
	assert.Equal(t, "content", n.Content)
	assert.Len(t, n.Values, 2)
}

// ---------------------------------------------------------------------------
// request_common.go — BaseRequest
// ---------------------------------------------------------------------------

func TestBaseRequest(t *testing.T) {
	var b BaseRequest
	meta := b.GetTokenCountMeta()
	assert.Equal(t, types.TokenTypeTokenizer, meta.TokenType)
	assert.False(t, b.IsStream(nil))
	b.SetModelName("x") // no-op, must not panic
	// BaseRequest satisfies the Request interface
	var _ Request = &BaseRequest{}
}
