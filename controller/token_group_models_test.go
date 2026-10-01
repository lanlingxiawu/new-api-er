package controller

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callGroupModels(t *testing.T, group string, setup func(ctx *gin.Context)) apiResp {
	t.Helper()
	ctx, rec := newCtx(t, http.MethodGet, "/api/group/x/models", nil)
	ctx.Params = gin.Params{{Key: "group", Value: group}}
	setup(ctx)
	GetAvailableModelsByGroup(ctx)
	return decodeResp(t, rec)
}

func mkGroupModelsLog(t *testing.T, group, modelName string, logType int, createdAt int64) {
	t.Helper()
	entry := &model.Log{Group: group, ModelName: modelName, Type: logType, CreatedAt: createdAt, Username: uniq("gm")}
	require.NoError(t, model.LOG_DB.Create(entry).Error)
	t.Cleanup(func() { model.LOG_DB.Delete(&model.Log{}, entry.Id) })
}

func TestGetAvailableModelsByGroup_EmptyGroupRejected(t *testing.T) {
	resp := callGroupModels(t, "  ", func(ctx *gin.Context) { asRoot(ctx, nextTestID()) })
	assert.False(t, resp.Success)
}

// A common user cannot probe a group outside their usable groups (hidden
// groups must not be enumerable); the check runs before any DB access.
func TestGetAvailableModelsByGroup_CommonUserOutsideUsableGroupsDenied(t *testing.T) {
	ctx, rec := newCtx(t, http.MethodGet, "/api/group/x/models", nil)
	hidden := uniq("hidden")
	ctx.Params = gin.Params{{Key: "group", Value: hidden}}
	asUser(ctx, nextTestID())
	ctx.Set("group", uniq("own"))
	GetAvailableModelsByGroup(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgDistributorGroupAccessDenied), resp.Message)
}

// Only models of the group's channels with a consume log in this group inside
// the last 30 minutes are listed, once each, sorted.
func TestGetAvailableModelsByGroup_ListsRecentlyConsumedModels(t *testing.T) {
	requireDB(t)
	requireLogDB(t)
	group := uniq("gm")
	mkChannel(t, func(ch *model.Channel) { ch.Group = group; ch.Models = "m-b,m-a,m-old" })
	mkChannel(t, func(ch *model.Channel) { ch.Group = group; ch.Models = "m-a,m-err,,m-other-group" })
	now := time.Now().Unix()
	mkGroupModelsLog(t, group, "m-a", model.LogTypeConsume, now)
	mkGroupModelsLog(t, group, "m-a", model.LogTypeConsume, now-60) // duplicate row, listed once
	mkGroupModelsLog(t, group, "m-b", model.LogTypeConsume, now-29*60)
	mkGroupModelsLog(t, group, "m-old", model.LogTypeConsume, now-31*60)
	mkGroupModelsLog(t, group, "m-err", model.LogTypeError, now)
	mkGroupModelsLog(t, uniq("other"), "m-other-group", model.LogTypeConsume, now)
	mkGroupModelsLog(t, group, "m-not-on-channel", model.LogTypeConsume, now)

	check := func(resp apiResp) {
		require.True(t, resp.Success, resp.Message)
		var out dto.AvailableModelsResponse
		require.NoError(t, common.Unmarshal(resp.Data, &out))
		assert.Equal(t, []string{"m-a", "m-b"}, out.Models)
		assert.Equal(t, 2, out.Count)
	}
	// The user's own group is always usable.
	check(callGroupModels(t, group, func(ctx *gin.Context) {
		asUser(ctx, nextTestID())
		ctx.Set("group", group)
	}))
	// Administrators may ask about any group.
	check(callGroupModels(t, group, func(ctx *gin.Context) {
		asAdmin(ctx, nextTestID())
		ctx.Set("group", uniq("own"))
	}))
}

// stubAvailableModels replaces the uncached lookup with a counting stub and a
// controllable clock, and resets the cache around the test.
func stubAvailableModels(t *testing.T, load func(group string) ([]string, error)) (calls *atomic.Int64, now *time.Time) {
	t.Helper()
	calls = &atomic.Int64{}
	clock := time.Unix(1_700_000_000, 0)
	now = &clock
	prevLoad, prevNow := loadAvailableModelsFn, availableModelsNow
	reset := func() {
		availableModelsCacheMu.Lock()
		availableModelsCache = make(map[string]availableModelsCacheEntry)
		availableModelsCacheMu.Unlock()
	}
	reset()
	// Tests only move the clock between calls, never while callers run.
	availableModelsNow = func() time.Time { return *now }
	loadAvailableModelsFn = func(group string) ([]string, error) {
		calls.Add(1)
		return load(group)
	}
	t.Cleanup(func() {
		loadAvailableModelsFn, availableModelsNow = prevLoad, prevNow
		reset()
	})
	return calls, now
}

func groupModelsOf(t *testing.T, resp apiResp) []string {
	t.Helper()
	require.True(t, resp.Success, resp.Message)
	var out dto.AvailableModelsResponse
	require.NoError(t, common.Unmarshal(resp.Data, &out))
	assert.Equal(t, len(out.Models), out.Count)
	return out.Models
}

// Within the TTL a group is loaded once; other groups are cached separately.
func TestGetAvailableModelsByGroup_CacheHitWithinTTL(t *testing.T) {
	calls, _ := stubAvailableModels(t, func(group string) ([]string, error) { return []string{group + "-m"}, nil })
	admin := func(ctx *gin.Context) { asAdmin(ctx, nextTestID()) }

	assert.Equal(t, []string{"g1-m"}, groupModelsOf(t, callGroupModels(t, "g1", admin)))
	assert.Equal(t, []string{"g1-m"}, groupModelsOf(t, callGroupModels(t, "g1", admin)))
	assert.EqualValues(t, 1, calls.Load(), "second call within the TTL must be served from cache")

	assert.Equal(t, []string{"g2-m"}, groupModelsOf(t, callGroupModels(t, "g2", admin)))
	assert.EqualValues(t, 2, calls.Load(), "each group has its own entry")
}

// The entry is served until just before expiry and reloaded at expiry.
func TestGetAvailableModelsByGroup_CacheExpires(t *testing.T) {
	version := "v1"
	calls, now := stubAvailableModels(t, func(string) ([]string, error) { return []string{version}, nil })
	admin := func(ctx *gin.Context) { asAdmin(ctx, nextTestID()) }

	assert.Equal(t, []string{"v1"}, groupModelsOf(t, callGroupModels(t, "g", admin)))
	version = "v2"
	*now = now.Add(availableModelsCacheTTL - time.Nanosecond)
	assert.Equal(t, []string{"v1"}, groupModelsOf(t, callGroupModels(t, "g", admin)), "still fresh just before the TTL")
	*now = now.Add(time.Nanosecond)
	assert.Equal(t, []string{"v2"}, groupModelsOf(t, callGroupModels(t, "g", admin)), "reloaded at the TTL")
	assert.EqualValues(t, 2, calls.Load())
}

// Errors are not cached: the next call retries the query.
func TestGetAvailableModelsByGroup_ErrorNotCached(t *testing.T) {
	fail := true
	calls, _ := stubAvailableModels(t, func(string) ([]string, error) {
		if fail {
			return nil, errors.New("db down")
		}
		return []string{"m"}, nil
	})
	admin := func(ctx *gin.Context) { asAdmin(ctx, nextTestID()) }

	resp := callGroupModels(t, "g", admin)
	assert.False(t, resp.Success)
	assert.NotContains(t, resp.Message, "db down", "internal errors are not shown to the user")
	fail = false
	assert.Equal(t, []string{"m"}, groupModelsOf(t, callGroupModels(t, "g", admin)))
	assert.EqualValues(t, 2, calls.Load())
}

// Concurrent misses for one group share a single query.
func TestGetAvailableModelsByGroup_ConcurrentMissesShareOneQuery(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 64)
	calls, _ := stubAvailableModels(t, func(string) ([]string, error) {
		entered <- struct{}{}
		<-release
		return []string{"m"}, nil
	})

	const callers = 20
	var wg sync.WaitGroup
	results := make([][]string, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = availableModelsForGroup("g")
		}(i)
	}
	<-entered // the leader is inside the query; give the others time to queue behind it
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.EqualValues(t, 1, calls.Load())
	for i := 0; i < callers; i++ {
		require.NoError(t, errs[i])
		assert.Equal(t, []string{"m"}, results[i])
	}
}

// A warm cache must not let a common user read a group outside their usable
// groups: the permission check runs first and the loader is never reached.
func TestGetAvailableModelsByGroup_PermissionCheckedBeforeCache(t *testing.T) {
	calls, _ := stubAvailableModels(t, func(string) ([]string, error) { return []string{"secret-model"}, nil })
	hidden := uniq("hidden")
	groupModelsOf(t, callGroupModels(t, hidden, func(ctx *gin.Context) { asAdmin(ctx, nextTestID()) }))
	require.EqualValues(t, 1, calls.Load())

	resp := callGroupModels(t, hidden, func(ctx *gin.Context) {
		asUser(ctx, nextTestID())
		ctx.Set("group", uniq("own"))
	})
	assert.False(t, resp.Success)
	assert.NotContains(t, string(resp.Data), "secret-model")
	assert.EqualValues(t, 1, calls.Load())
}

// The map never grows past its bound; expired entries are evicted first.
func TestStoreAvailableModels_Bounded(t *testing.T) {
	_, now := stubAvailableModels(t, func(string) ([]string, error) { return nil, nil })
	for i := 0; i < availableModelsCacheMaxGroups; i++ {
		storeAvailableModels(fmt.Sprintf("old-%d", i), []string{})
	}
	*now = now.Add(availableModelsCacheTTL)
	storeAvailableModels("fresh", []string{"m"})
	availableModelsCacheMu.Lock()
	assert.Len(t, availableModelsCache, 1, "expired entries are evicted when the map is full")
	availableModelsCacheMu.Unlock()

	for i := 0; i < availableModelsCacheMaxGroups+10; i++ {
		storeAvailableModels(fmt.Sprintf("live-%d", i), []string{})
	}
	availableModelsCacheMu.Lock()
	assert.Len(t, availableModelsCache, availableModelsCacheMaxGroups, "live entries never exceed the bound")
	availableModelsCacheMu.Unlock()
	// Refreshing an existing key when full does not evict anything.
	storeAvailableModels("live-0", []string{"x"})
	availableModelsCacheMu.Lock()
	assert.Len(t, availableModelsCache, availableModelsCacheMaxGroups)
	availableModelsCacheMu.Unlock()
}

func TestGetAvailableModelsByGroup_GroupWithoutChannelsIsEmpty(t *testing.T) {
	requireDB(t)
	resp := callGroupModels(t, uniq("nochan"), func(ctx *gin.Context) { asAdmin(ctx, nextTestID()) })
	require.True(t, resp.Success, resp.Message)
	var out dto.AvailableModelsResponse
	require.NoError(t, common.Unmarshal(resp.Data, &out))
	assert.Empty(t, out.Models)
	assert.NotNil(t, out.Models)
	assert.Equal(t, 0, out.Count)
}
