package common

import (
	"html/template"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetIpDoesNotPanic(t *testing.T) {
	// Environment-dependent; assert only that it returns a parseable value or "".
	ip := GetIp()
	if ip != "" {
		assert.True(t, IsIP(ip), "GetIp returned non-IP %q", ip)
	}
}

func TestGetNetworkIps(t *testing.T) {
	ips := GetNetworkIps()
	for _, ip := range ips {
		assert.True(t, IsIP(ip))
		assert.True(t,
			strings.HasPrefix(ip, "10.") ||
				strings.HasPrefix(ip, "172.") ||
				strings.HasPrefix(ip, "192.168."),
			"unexpected non-private ip %q", ip)
	}
}

func TestIsRunningInContainer(t *testing.T) {
	// On the Windows dev/test host none of the container indicators exist.
	// Assert it returns without panicking; value is environment-dependent.
	_ = IsRunningInContainer()

	// Exercise the env-var branch deterministically.
	require.NoError(t, os.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1"))
	t.Cleanup(func() { _ = os.Unsetenv("KUBERNETES_SERVICE_HOST") })
	assert.True(t, IsRunningInContainer())
}

func TestBytes2Size(t *testing.T) {
	assert.Equal(t, "0 B", Bytes2Size(0))
	assert.Equal(t, "512 B", Bytes2Size(512))
	assert.Equal(t, "2 KB", Bytes2Size(2*1024))
	assert.Equal(t, "5 MB", Bytes2Size(5*1024*1024))
	assert.Equal(t, "2.00 GB", Bytes2Size(2*1024*1024*1024))
}

func TestSeconds2Time(t *testing.T) {
	assert.Equal(t, "0 秒", Seconds2Time(0))
	assert.Equal(t, "59 秒", Seconds2Time(59))
	assert.Equal(t, "1 分钟 0 秒", Seconds2Time(60))
	assert.Equal(t, "1 小时 0 秒", Seconds2Time(3600))
	assert.Equal(t, "1 天 0 秒", Seconds2Time(86400))
	assert.Equal(t, "1 个月 0 秒", Seconds2Time(2592000))
	assert.Equal(t, "1 年 0 秒", Seconds2Time(31104000))
	// mixed value exercises every component together
	assert.Equal(t, "1 年 1 个月 1 天 1 小时 1 分钟 1 秒",
		Seconds2Time(31104000+2592000+86400+3600+60+1))
}

func TestInterface2String(t *testing.T) {
	assert.Equal(t, "hello", Interface2String("hello"))
	assert.Equal(t, "42", Interface2String(42))
	assert.Equal(t, "3.14", Interface2String(3.14))
	assert.Equal(t, "true", Interface2String(true))
	assert.Equal(t, "false", Interface2String(false))
	assert.Equal(t, "", Interface2String(nil))
	// fallthrough to %v for an unhandled type
	assert.Equal(t, "[1 2]", Interface2String([]int{1, 2}))
}

func TestUnescapeHTML(t *testing.T) {
	out := UnescapeHTML("<b>x</b>")
	assert.Equal(t, template.HTML("<b>x</b>"), out)
}

func TestIntMaxAndMax(t *testing.T) {
	assert.Equal(t, 5, IntMax(5, 3))
	assert.Equal(t, 5, IntMax(3, 5))
	assert.Equal(t, 5, IntMax(5, 5))
	assert.Equal(t, 5, Max(5, 3))
	assert.Equal(t, 5, Max(3, 5))
}

func TestGetUUID(t *testing.T) {
	u := GetUUID()
	assert.Len(t, u, 32)
	assert.NotContains(t, u, "-")
	assert.NotEqual(t, u, GetUUID())
}

func TestGenerateRandomCharsKey(t *testing.T) {
	k, err := GenerateRandomCharsKey(48)
	require.NoError(t, err)
	assert.Len(t, k, 48)
	for _, r := range k {
		assert.True(t, strings.ContainsRune(keyChars, r))
	}

	k0, err := GenerateRandomCharsKey(0)
	require.NoError(t, err)
	assert.Equal(t, "", k0)
}

func TestGenerateRandomKey(t *testing.T) {
	k, err := GenerateRandomKey(48)
	require.NoError(t, err)
	// base64 of 36 raw bytes = 48 chars
	assert.Len(t, k, 48)
}

func TestGenerateKey(t *testing.T) {
	k, err := GenerateKey()
	require.NoError(t, err)
	assert.Len(t, k, 48)
}

func TestGetRandomInt(t *testing.T) {
	for i := 0; i < 50; i++ {
		v := GetRandomInt(10)
		assert.GreaterOrEqual(t, v, 0)
		assert.Less(t, v, 10)
	}
}

func TestGetTimestamp(t *testing.T) {
	assert.Positive(t, GetTimestamp())
}

func TestGetTimeString(t *testing.T) {
	s := GetTimeString()
	// 14-digit date-time prefix + 9-digit nanos = 23 chars
	assert.Len(t, s, 23)
}

func TestNewRequestId(t *testing.T) {
	id := NewRequestId()
	// TimeString(23) + prefix(8) + random(8) = 39
	assert.Len(t, id, 39)
	assert.NotEqual(t, id, NewRequestId())
}

func TestStripRequestIds(t *testing.T) {
	assert.Equal(t, "upstream failed",
		StripRequestIds("upstream failed (request id: abc)"))
	assert.Equal(t, "upstream failed",
		StripRequestIds("upstream failed (request id: a) (request id: b)"))
	assert.Equal(t, "no marker", StripRequestIds("no marker"))
	assert.Equal(t, "", StripRequestIds("(request id: only)"))
}

func TestMessageWithRequestId(t *testing.T) {
	assert.Equal(t, "boom (request id: cur)",
		MessageWithRequestId("boom", "cur"))
	// nested upstream ids stripped, only the current id kept
	out := MessageWithRequestId("upstream failed (request id: a) (request id: b)", "cur")
	assert.Equal(t, "upstream failed (request id: cur)", out)
	assert.Equal(t, 1, strings.Count(out, "(request id:"))
	// empty message after stripping -> just the id marker
	assert.Equal(t, "(request id: cur)",
		MessageWithRequestId("(request id: x)", "cur"))
}

func TestGetPointer(t *testing.T) {
	p := GetPointer(7)
	require.NotNil(t, p)
	assert.Equal(t, 7, *p)
}

func TestAny2Type(t *testing.T) {
	type target struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	got, err := Any2Type[target](map[string]any{"a": 1, "b": "x"})
	require.NoError(t, err)
	assert.Equal(t, target{A: 1, B: "x"}, got)

	// marshal error: channels cannot be marshaled -> zero value + err
	_, err = Any2Type[target](make(chan int))
	assert.Error(t, err)

	// unmarshal error: source type incompatible with target
	_, err = Any2Type[target]("a plain string")
	assert.Error(t, err)
}

func TestSaveTmpFile(t *testing.T) {
	path, err := SaveTmpFile("unit-test-*.txt", strings.NewReader("payload"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(path) })
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(data))
}

func TestBuildURL(t *testing.T) {
	assert.Equal(t, "https://api.test.com/v1/chat",
		BuildURL("https://api.test.com", "/v1/chat"))
	// empty endpoint resolves to base root
	assert.Equal(t, "https://api.test.com/",
		BuildURL("https://api.test.com", ""))
	// relative endpoint resolved against base path
	assert.Equal(t, "https://api.test.com/v1/x",
		BuildURL("https://api.test.com/v1/", "x"))
}
