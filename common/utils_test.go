package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageWithRequestIdStripsNestedRequestIds(t *testing.T) {
	message := "upstream failed (request id: upstream-a) (request id: upstream-b)"

	result := MessageWithRequestId(message, "current-id")

	require.Equal(t, "upstream failed (request id: current-id)", result)
	require.NotContains(t, result, "upstream-a")
	require.NotContains(t, result, "upstream-b")
	require.Equal(t, 1, strings.Count(result, "(request id:"))
}

func TestStripRequestIdsTrimsEmptyMessage(t *testing.T) {
	require.Equal(t, "", StripRequestIds(" (request id: upstream-a) "))
	require.Equal(t, "(request id: current-id)", MessageWithRequestId(" (request id: upstream-a) ", "current-id"))
}
