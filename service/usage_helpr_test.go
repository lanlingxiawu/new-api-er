package service

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gin-gonic/gin"
)

func TestResponseText2UsageFromStreamReturnsZeroUsageWhenNoSSEData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	usage := ResponseText2UsageFromStream(c, "hello", "gpt-4o", 123, 0)

	require.NotNil(t, usage)
	require.Zero(t, usage.PromptTokens)
	require.Zero(t, usage.CompletionTokens)
	require.Zero(t, usage.TotalTokens)
}

func TestResponseText2UsageFromStreamUsesEstimateWhenStreamHasData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	usage := ResponseText2UsageFromStream(c, "hello", "gpt-4o", 123, 1)

	require.NotNil(t, usage)
	require.Equal(t, 123, usage.PromptTokens)
	require.NotZero(t, usage.CompletionTokens)
	require.Equal(t, 123+usage.CompletionTokens, usage.TotalTokens)
}
