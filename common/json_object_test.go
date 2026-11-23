package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnmarshalJSONObjectRejectsAmbiguousDocuments(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"id":1,"id":2}`, `{"id":1,"\u0069d":2}`, `{"id":1} {}`, `{"id":1}junk`} {
		_, err := UnmarshalJSONObject([]byte(raw))
		require.Error(t, err, raw)
	}
	fields, err := UnmarshalJSONObject([]byte(" {\"value\":0,\"data\":{\"x\":1}} \n"))
	require.NoError(t, err)
	require.Equal(t, "0", string(fields["value"]))
	require.Len(t, fields, 2)
}
