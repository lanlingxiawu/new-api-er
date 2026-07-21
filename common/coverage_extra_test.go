package common

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errReader always fails, used to exercise io.Copy error branches.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom reader") }

func TestSaveTmpFile_CopyError(t *testing.T) {
	_, err := SaveTmpFile("unit-test-copyerr-*.txt", errReader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to copy data")
}

func TestBuildURL_ParseErrors(t *testing.T) {
	// A control character makes url.Parse fail, so BuildURL falls back to
	// plain concatenation for both the base and the endpoint parse steps.
	assert.Equal(t, "http://bad\x7f/x", BuildURL("http://bad\x7f", "/x"))
	assert.Equal(t, "https://ok.com\x7f", BuildURL("https://ok.com", "\x7f"))
}

func TestProcessFormMap_UnmarshalError(t *testing.T) {
	// form value is a string but the target field is an int -> unmarshal error
	var out struct {
		A int `json:"a"`
	}
	err := parseFormData([]byte("a=not-an-int"), &out)
	require.Error(t, err)
}

// TestRedisDebugLoggingBranches re-runs the happy paths with DebugEnabled=true
// so the debug SysLog branches inside every Redis helper are executed.
func TestRedisDebugLoggingBranches(t *testing.T) {
	requireRedis(t)
	orig := DebugEnabled
	DebugEnabled = true
	t.Cleanup(func() { DebugEnabled = orig })

	key := "test:common:debug:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisSet(key, "1", time.Minute))
	_, err := RedisGet(key)
	require.NoError(t, err)
	require.NoError(t, RedisIncr(key, 1))
	require.NoError(t, RedisDelKey(key))

	hkey := "test:common:debughash:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(hkey) })
	require.NoError(t, RedisHSetObj(hkey, &redisTestObj{Name: "n", Count: 5}, time.Minute))
	require.NoError(t, RedisHGetObj(hkey, &redisTestObj{}))
	require.NoError(t, RedisHIncrBy(hkey, "Count", 1))
	require.NoError(t, RedisHSetField(hkey, "Name", "m"))
	require.NoError(t, RedisDel(hkey))
}

// TestRedisHGetObj_FieldTypes covers the Int64 case and the unsupported-type
// default error branch of the field decoder.
type redisInt64Obj struct {
	Big int64
}

type redisFloatObj struct {
	F float64
}

func TestRedisHGetObj_Int64Field(t *testing.T) {
	requireRedis(t)
	key := "test:common:i64:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisHSetObj(key, &redisInt64Obj{Big: 9000000000}, time.Minute))
	var out redisInt64Obj
	require.NoError(t, RedisHGetObj(key, &out))
	assert.Equal(t, int64(9000000000), out.Big)
}

func TestRedisHGetObj_UnsupportedFieldType(t *testing.T) {
	requireRedis(t)
	key := "test:common:float:" + GetRandomString(8)
	t.Cleanup(func() { _ = RedisDel(key) })

	require.NoError(t, RedisHSetObj(key, &redisFloatObj{F: 1.5}, time.Minute))
	var out redisFloatObj
	err := RedisHGetObj(key, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported field type")
}
