package helper_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndependentAuditValidRequestSemantics(t *testing.T) {
	oldDisk, oldValidation := common.GetDiskCacheConfig(), common.GetRequestValidationConfig()
	t.Cleanup(func() {
		common.SetDiskCacheConfig(oldDisk)
		common.SetRequestValidationConfig(oldValidation)
	})
	for _, disk := range []bool{false, true} {
		for _, strict := range []bool{false, true} {
			t.Run(fmt.Sprintf("disk=%t/strict=%t", disk, strict), func(t *testing.T) {
				common.SetDiskCacheConfig(common.DiskCacheConfig{
					GovernanceEnabled: true, MemoryBudgetMB: 16, Enabled: disk,
					ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir(),
				})
				common.SetRequestValidationConfig(common.RequestValidationConfig{StrictJSONEnabled: strict})
				for _, test := range []struct {
					name, path, modelParam, contentType, body, expected, tokenText string
					format                                                         types.RelayFormat
				}{
					{
						name: "embedding_explicit_zero", path: "/v1/embeddings", format: types.RelayFormatEmbedding,
						body:      `{"model":"audit","input":["hello","world"],"dimensions":0,"seed":0,"temperature":0,"top_p":0,"frequency_penalty":0,"presence_penalty":0,"encoding_format":"float","user":"u","vendor":{"enabled":false}}`,
						expected:  `{"model":"audit","input":["hello","world"],"dimensions":0,"seed":0,"temperature":0,"top_p":0,"frequency_penalty":0,"presence_penalty":0,"encoding_format":"float","user":"u"}`,
						tokenText: "hello\nworld",
					},
					{
						name: "embedding_omitted_options", path: "/v1/embeddings", format: types.RelayFormatEmbedding,
						body: `{"model":"audit","input":"hello"}`, expected: `{"model":"audit","input":"hello"}`, tokenText: "hello",
					},
					{
						name: "moderation_default_model", path: "/v1/moderations", format: types.RelayFormatEmbedding,
						body: `{"input":"hello"}`, expected: `{"model":"omni-moderation-latest","input":"hello"}`, tokenText: "hello",
					},
					{
						name: "embedding_route_model", path: "/openai/deployments/audit/embeddings", modelParam: "audit", format: types.RelayFormatEmbedding,
						body: `{"input":"hello"}`, expected: `{"model":"audit","input":"hello"}`, tokenText: "hello",
					},
					{
						name: "rerank_explicit_zero_false", path: "/v1/rerank", format: types.RelayFormatRerank,
						body:      `{"model":"audit","query":"q","documents":["hello"],"top_n":0,"return_documents":false,"max_chunk_per_doc":0,"overlap_tokens":0}`,
						expected:  `{"model":"audit","query":"q","documents":["hello"],"top_n":0,"return_documents":false,"max_chunk_per_doc":0,"overlap_tokens":0}`,
						tokenText: "hello\nq",
					},
					{
						name: "rerank_object_document", path: "/v1/rerank", format: types.RelayFormatRerank,
						body: `{"model":"audit","query":"q","documents":[{"text":"hello"}]}`, expected: `{"model":"audit","query":"q","documents":[{"text":"hello"}]}`, tokenText: "map[text:hello]\nq",
					},
					{
						name: "embedding_form", path: "/v1/embeddings", format: types.RelayFormatEmbedding, contentType: "application/x-www-form-urlencoded",
						body: "model=audit&input=hello", expected: `{"model":"audit","input":"hello"}`, tokenText: "hello",
					},
					{
						name: "rerank_form", path: "/v1/rerank", format: types.RelayFormatRerank, contentType: "application/x-www-form-urlencoded",
						body: "model=audit&query=q&documents=hello&documents=world", expected: `{"model":"audit","query":"q","documents":["hello","world"]}`, tokenText: "hello\nworld\nq",
					},
				} {
					t.Run(test.name, func(t *testing.T) {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
						contentType := test.contentType
						if contentType == "" {
							contentType = "application/json; charset=utf-8"
						}
						c.Request.Header.Set("Content-Type", contentType)
						c.Params = gin.Params{{Key: "model", Value: test.modelParam}}
						t.Cleanup(func() { common.CleanupBodyStorage(c) })
						for range 2 {
							request, err := helper.GetAndValidateRequest(c, test.format)
							require.NoError(t, err)
							encoded, err := common.Marshal(request)
							require.NoError(t, err)
							assert.JSONEq(t, test.expected, string(encoded))
							assert.Equal(t, test.tokenText, request.GetTokenCountMeta().CombineText)
							body, err := io.ReadAll(c.Request.Body)
							require.NoError(t, err)
							assert.Equal(t, test.body, string(body), "replay must retain even unknown provider fields")
						}
					})
				}
			})
		}
	}
}

// These cases are derived from the request contract and the decoder call sites,
// independently of existing fixtures. Storage placement must not change JSON
// validity, and untrusted input must return an error rather than panic.
func TestIndependentAuditRequestValidation(t *testing.T) {
	require.NoError(t, i18n.Init())
	previous := common.GetDiskCacheConfig()
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	previousValidation := common.GetRequestValidationConfig()
	common.SetRequestValidationConfig(common.RequestValidationConfig{StrictJSONEnabled: true})
	t.Cleanup(func() { common.SetRequestValidationConfig(previousValidation) })
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprintf("disk=%t", disk), func(t *testing.T) {
			common.SetDiskCacheConfig(common.DiskCacheConfig{
				GovernanceEnabled: true, MemoryBudgetMB: 16,
				Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir(),
			})
			for _, endpoint := range []struct {
				path   string
				format types.RelayFormat
				valid  string
			}{
				{"/v1/embeddings", types.RelayFormatEmbedding, `{"model":"audit","input":"hello"}`},
				{"/v1/rerank", types.RelayFormatRerank, `{"model":"audit","query":"hello","documents":["hello"]}`},
				{"/v1/chat/completions", types.RelayFormatOpenAI, `{"model":"audit","messages":[{"role":"user","content":"hello"}]}`},
				{"/v1/responses", types.RelayFormatOpenAIResponses, `{"model":"audit","input":"hello"}`},
				{"/v1/messages", types.RelayFormatClaude, `{"model":"audit","messages":[{"role":"user","content":"hello"}]}`},
				{"/v1beta/models/audit:generateContent", types.RelayFormatGemini, `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
			} {
				t.Run(endpoint.path, func(t *testing.T) {
					for _, input := range []struct {
						name, body, contentType string
						valid                   bool
					}{
						{"valid", endpoint.valid, "application/json", true},
						{"whitespace", endpoint.valid + " \r\n\t", "application/json", true},
						{"null", "null", "application/json", false},
						{"array", "[]", "application/json", false},
						{"empty_object", "{}", "application/json", false},
						{"empty_body", "", "application/json", false},
						{"truncated", `{"model":`, "application/json", false},
						{"second_document", endpoint.valid + `{}`, "application/json", false},
						{"trailing_garbage", endpoint.valid + `x`, "application/json", false},
						{"unsupported_media", endpoint.valid, "text/plain", false},
					} {
						t.Run(input.name, func(t *testing.T) {
							c, _ := gin.CreateTestContext(httptest.NewRecorder())
							c.Request = httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(input.body))
							c.Request.Header.Set("Content-Type", input.contentType)
							t.Cleanup(func() { common.CleanupBodyStorage(c) })
							storage, err := common.GetBodyStorage(c)
							require.NoError(t, err)
							if input.body != "" {
								require.Equal(t, disk, storage.IsDisk())
							}
							assert.NotPanics(t, func() {
								request, validationErr := helper.GetAndValidateRequest(c, endpoint.format)
								if input.valid {
									require.NoError(t, validationErr)
									require.NotNil(t, request)
								} else {
									require.Error(t, validationErr, "invalid client input must be rejected")
									apiErr := helper.NewInvalidRequestError(c, validationErr)
									assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
									assert.True(t, types.IsSkipRetryError(apiErr))
									if input.name == "second_document" || input.name == "trailing_garbage" {
										assert.Equal(t, i18n.Translate(i18n.DefaultLang, i18n.MsgRelayRequestBodyInvalid), apiErr.Error())
										assert.NotContains(t, apiErr.Error(), "unexpected data after JSON document")
									}
								}
							})
						})
					}
				})
			}
		})
	}
}
