package common

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEnvOrDefault(t *testing.T) {
	const key = "COMMON_TEST_ENV_INT"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	// empty env name -> default
	assert.Equal(t, 7, GetEnvOrDefault("", 7))

	// unset -> default
	_ = os.Unsetenv(key)
	assert.Equal(t, 7, GetEnvOrDefault(key, 7))

	// valid int -> parsed
	require.NoError(t, os.Setenv(key, "42"))
	assert.Equal(t, 42, GetEnvOrDefault(key, 7))

	// invalid int -> default
	require.NoError(t, os.Setenv(key, "notint"))
	assert.Equal(t, 7, GetEnvOrDefault(key, 7))
}

func TestGetEnvOrDefaultString(t *testing.T) {
	const key = "COMMON_TEST_ENV_STR"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	assert.Equal(t, "d", GetEnvOrDefaultString("", "d"))

	_ = os.Unsetenv(key)
	assert.Equal(t, "d", GetEnvOrDefaultString(key, "d"))

	require.NoError(t, os.Setenv(key, "value"))
	assert.Equal(t, "value", GetEnvOrDefaultString(key, "d"))
}

func TestGetEnvOrDefaultBool(t *testing.T) {
	const key = "COMMON_TEST_ENV_BOOL"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	assert.True(t, GetEnvOrDefaultBool("", true))

	_ = os.Unsetenv(key)
	assert.False(t, GetEnvOrDefaultBool(key, false))

	require.NoError(t, os.Setenv(key, "true"))
	assert.True(t, GetEnvOrDefaultBool(key, false))

	require.NoError(t, os.Setenv(key, "notbool"))
	assert.True(t, GetEnvOrDefaultBool(key, true)) // parse error -> default
}
