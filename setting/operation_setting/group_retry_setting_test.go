package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRetryTimesTenAttemptLimit(t *testing.T) {
	for _, input := range []string{`{"g":-1}`, `{"g":0}`, `{"g":1}`, `{"g":9}`, `{"g":10}`} {
		require.NoError(t, CheckGroupRetryTimes(input), input)
	}
	for _, input := range []string{`{"g":-2}`, `{"g":11}`, `{"g":20}`, `{"g":1.5}`, `{"g":"10"}`} {
		require.Error(t, CheckGroupRetryTimes(input), input)
	}
}
