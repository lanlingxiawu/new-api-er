package common_test

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditJSONErrorReader struct {
	data string
	err  error
}

type auditStalledJSONReader struct{}

func (auditStalledJSONReader) Read([]byte) (int, error) { return 0, nil }

func (r *auditJSONErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if r.data == "" {
		return n, r.err
	}
	return n, nil
}

func TestAuditSingleJSONDocument(t *testing.T) {
	for _, test := range []struct {
		name, input string
		valid       bool
	}{
		{"object", `{"value":0}`, true},
		{"whitespace", " \t\n{\"value\":0}\r\n ", true},
		{"long_whitespace", `{"value":0}` + strings.Repeat(" \r\n\t", 8192), true},
		{"second_object", `{"value":0}{}`, false},
		{"second_null", `{"value":0} null`, false},
		{"far_tail", `{"value":0}` + strings.Repeat(" ", 32768) + "x", false},
		{"non_json_space", "{\"value\":0}\v", false},
		{"empty", "", false},
		{"truncated", `{"value":`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, reader := range []io.Reader{strings.NewReader(test.input), iotest.OneByteReader(strings.NewReader(test.input))} {
				var value struct{ Value int }
				err := common.DecodeSingleJson(reader, &value)
				if test.valid {
					require.NoError(t, err)
					assert.Zero(t, value.Value)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
	for _, data := range []string{`{"value":0}`, `{"value":0}` + strings.Repeat(" ", 8192)} {
		var value any
		require.ErrorIs(t, common.DecodeSingleJson(&auditJSONErrorReader{data: data, err: io.ErrUnexpectedEOF}, &value), io.ErrUnexpectedEOF)
		require.NoError(t, common.DecodeSingleJson(&auditJSONErrorReader{data: data, err: io.EOF}, &value))
	}
	var value any
	require.NoError(t, common.DecodeJson(strings.NewReader(`{} {}`), &value), "general stream decoding stays compatible")
	require.ErrorIs(t, common.DecodeSingleJson(auditStalledJSONReader{}, &value), io.ErrNoProgress)
	require.ErrorIs(t, common.DecodeSingleJson(io.MultiReader(strings.NewReader(`{}`), auditStalledJSONReader{}), &value), io.ErrNoProgress)
	largeTail := strings.NewReader(`{} {"large":"` + strings.Repeat("x", 1<<20) + `"}`)
	initialLength := largeTail.Len()
	require.Error(t, common.DecodeSingleJson(largeTail, &value))
	assert.LessOrEqual(t, initialLength-largeTail.Len(), 4096, "reject the first trailing byte without decoding another large value")
}

func BenchmarkAuditJSONDocument(b *testing.B) {
	payload := `{"value":"` + strings.Repeat("x", 1024) + `"} `
	for _, test := range []struct {
		name   string
		decode func(io.Reader, any) error
	}{
		{"stream", common.DecodeJson},
		{"single", common.DecodeSingleJson},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				var value struct{ Value string }
				if err := test.decode(strings.NewReader(payload), &value); err != nil {
					require.NoError(b, err)
				}
			}
		})
	}
}

func TestAuditStrictRequestJSONSwitchAndReplay(t *testing.T) {
	oldDisk := common.GetDiskCacheConfig()
	oldValidation := common.GetRequestValidationConfig()
	t.Cleanup(func() {
		common.SetDiskCacheConfig(oldDisk)
		common.SetRequestValidationConfig(oldValidation)
	})
	for _, disk := range []bool{false, true} {
		common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
		for _, enabled := range []bool{false, true} {
			common.SetRequestValidationConfig(common.RequestValidationConfig{StrictJSONEnabled: enabled})
			for _, tail := range []string{" \n", `{}`, strings.Repeat(" ", 8192) + "x"} {
				body := `{"value":0}` + tail
				before := common.GetDiskCacheStats()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				for range 2 {
					var value any
					err := common.UnmarshalBodyReusable(c, &value)
					rejected := tail != " \n" && (!disk || enabled)
					if rejected {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						got, err := io.ReadAll(c.Request.Body)
						require.NoError(t, err)
						assert.Equal(t, body, string(got))
					}
				}
				common.CleanupBodyStorage(c)
				after := common.GetDiskCacheStats()
				assert.Equal(t, before.BodyMemoryReservedBytes, after.BodyMemoryReservedBytes)
				assert.Equal(t, before.DiskReservedBytes, after.DiskReservedBytes)
				assert.Equal(t, before.ActiveDiskFiles, after.ActiveDiskFiles)
			}
		}
	}
}

func FuzzAuditSingleJSONDocument(f *testing.F) {
	for _, input := range []string{`{}`, `{} {}`, `{"x":0}`, "null", "\"text\"", "0 ", "[]\t", "\xff"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var memory, streamed any
		want := common.Unmarshal([]byte(input), &memory)
		got := common.DecodeSingleJson(strings.NewReader(input), &streamed)
		assert.Equal(t, want == nil, got == nil)
		if want == nil {
			assert.Equal(t, memory, streamed)
		}
		assert.False(t, errors.Is(got, io.ErrNoProgress))
	})
}
