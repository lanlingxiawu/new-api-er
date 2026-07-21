package setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveRateLimitGroup(t *testing.T) {
	t.Helper()
	orig := ModelRequestRateLimitGroup
	t.Cleanup(func() { ModelRequestRateLimitGroup = orig })
}

func TestModelRequestRateLimitGroup2JSONString(t *testing.T) {
	saveRateLimitGroup(t)
	ModelRequestRateLimitGroup = map[string][2]int{"vip": {10, 5}}
	assert.JSONEq(t, `{"vip":[10,5]}`, ModelRequestRateLimitGroup2JSONString())
}

func TestModelRequestRateLimitGroup2JSONString_Empty(t *testing.T) {
	saveRateLimitGroup(t)
	ModelRequestRateLimitGroup = map[string][2]int{}
	assert.Equal(t, `{}`, ModelRequestRateLimitGroup2JSONString())
}

func TestUpdateModelRequestRateLimitGroupByJSONString_Valid(t *testing.T) {
	saveRateLimitGroup(t)
	err := UpdateModelRequestRateLimitGroupByJSONString(`{"g":[3,7]}`)
	require.NoError(t, err)
	assert.Equal(t, [2]int{3, 7}, ModelRequestRateLimitGroup["g"])
}

func TestUpdateModelRequestRateLimitGroupByJSONString_Invalid(t *testing.T) {
	saveRateLimitGroup(t)
	err := UpdateModelRequestRateLimitGroupByJSONString(`nope`)
	require.Error(t, err)
	// Reset to a fresh (non-nil) empty map before unmarshal.
	assert.NotNil(t, ModelRequestRateLimitGroup)
	assert.Empty(t, ModelRequestRateLimitGroup)
}

func TestGetGroupRateLimit_Found(t *testing.T) {
	saveRateLimitGroup(t)
	ModelRequestRateLimitGroup = map[string][2]int{"vip": {100, 50}}
	total, success, found := GetGroupRateLimit("vip")
	assert.True(t, found)
	assert.Equal(t, 100, total)
	assert.Equal(t, 50, success)
}

func TestGetGroupRateLimit_NotFound(t *testing.T) {
	saveRateLimitGroup(t)
	ModelRequestRateLimitGroup = map[string][2]int{"vip": {1, 1}}
	total, success, found := GetGroupRateLimit("missing")
	assert.False(t, found)
	assert.Zero(t, total)
	assert.Zero(t, success)
}

func TestGetGroupRateLimit_NilMap(t *testing.T) {
	saveRateLimitGroup(t)
	ModelRequestRateLimitGroup = nil
	total, success, found := GetGroupRateLimit("any")
	assert.False(t, found)
	assert.Zero(t, total)
	assert.Zero(t, success)
}

func TestCheckModelRequestRateLimitGroup_Valid(t *testing.T) {
	// total==0 (lower valid boundary), success==1 (lower valid boundary).
	assert.NoError(t, CheckModelRequestRateLimitGroup(`{"g":[0,1]}`))
	assert.NoError(t, CheckModelRequestRateLimitGroup(`{"g":[5,10]}`))
}

func TestCheckModelRequestRateLimitGroup_InvalidJSON(t *testing.T) {
	require.Error(t, CheckModelRequestRateLimitGroup(`nope`))
}

func TestCheckModelRequestRateLimitGroup_NegativeTotal(t *testing.T) {
	err := CheckModelRequestRateLimitGroup(`{"g":[-1,5]}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative rate limit values")
}

func TestCheckModelRequestRateLimitGroup_SuccessBelowOne(t *testing.T) {
	// success==0 is invalid (must be >=1).
	err := CheckModelRequestRateLimitGroup(`{"g":[5,0]}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative rate limit values")
}

func TestCheckModelRequestRateLimitGroup_TotalOverMaxInt32(t *testing.T) {
	over := int64(math.MaxInt32) + 1
	err := CheckModelRequestRateLimitGroup(`{"g":[` + itoa(over) + `,5]}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max rate limits value 2147483647")
}

func TestCheckModelRequestRateLimitGroup_SuccessOverMaxInt32(t *testing.T) {
	over := int64(math.MaxInt32) + 1
	err := CheckModelRequestRateLimitGroup(`{"g":[5,` + itoa(over) + `]}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max rate limits value 2147483647")
}

func TestCheckModelRequestRateLimitGroup_MaxInt32Boundary(t *testing.T) {
	// Exactly MaxInt32 is still valid (boundary inside).
	max := itoa(int64(math.MaxInt32))
	assert.NoError(t, CheckModelRequestRateLimitGroup(`{"g":[`+max+`,`+max+`]}`))
}

// itoa is a tiny int64->string helper to keep the JSON literals readable.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
