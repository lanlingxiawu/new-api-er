package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uniqNodeName returns a unique node_name (primary key) for system_instances.
func uniqNodeName() string {
	return "node_" + strings.ReplaceAll(uniq("n"), "_", "")
}

func cleanupSystemInstance(t *testing.T, nodeName string) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Where("node_name = ?", nodeName).Delete(&SystemInstance{})
		}
	})
}

func containsInstance(list []*SystemInstance, nodeName string) bool {
	for _, i := range list {
		if i.NodeName == nodeName {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Pure logic: ToResponse status + info marshalling
// ---------------------------------------------------------------------------

func TestSystemInstance_ToResponse(t *testing.T) {
	now := common.GetTimestamp()

	// fresh -> online
	fresh := &SystemInstance{NodeName: "n1", StartedAt: now - 10, LastSeenAt: now - 5, Info: `{"v":"1.0"}`}
	resp := fresh.ToResponse(now)
	assert.Equal(t, "n1", resp.NodeName)
	assert.Equal(t, SystemInstanceStatusOnline, resp.Status)
	assert.Equal(t, SystemInstanceStaleAfterSeconds, resp.StaleAfterSeconds)
	assert.Equal(t, map[string]any{"v": "1.0"}, resp.Info)

	// last seen just at the boundary (== stale-after) is still online (> comparison)
	boundary := &SystemInstance{NodeName: "n2", LastSeenAt: now - SystemInstanceStaleAfterSeconds}
	assert.Equal(t, SystemInstanceStatusOnline, boundary.ToResponse(now).Status)

	// beyond boundary -> stale
	stale := &SystemInstance{NodeName: "n3", LastSeenAt: now - SystemInstanceStaleAfterSeconds - 1}
	assert.Equal(t, SystemInstanceStatusStale, stale.ToResponse(now).Status)

	// empty info decodes to nil
	empty := &SystemInstance{NodeName: "n4", LastSeenAt: now, Info: ""}
	assert.Nil(t, empty.ToResponse(now).Info)

	// non-JSON info decodes to the raw string
	raw := &SystemInstance{NodeName: "n5", LastSeenAt: now, Info: "not-json"}
	assert.Equal(t, "not-json", raw.ToResponse(now).Info)
}

func TestMarshalSystemInstanceInfo(t *testing.T) {
	s, err := marshalSystemInstanceInfo(nil)
	require.NoError(t, err)
	assert.Equal(t, "", s)

	s, err = marshalSystemInstanceInfo(map[string]any{"a": float64(1)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, s)
}

// ---------------------------------------------------------------------------
// DB: upsert / list / stale deletion
// ---------------------------------------------------------------------------

func TestUpsertSystemInstance(t *testing.T) {
	requireDB(t)
	node := uniqNodeName()
	cleanupSystemInstance(t, node)
	now := common.GetTimestamp()

	// insert
	require.NoError(t, UpsertSystemInstance(node, map[string]any{"v": "1.0"}, now-100, now-50))
	var got SystemInstance
	require.NoError(t, DB.Where("node_name = ?", node).First(&got).Error)
	assert.EqualValues(t, now-100, got.StartedAt)
	assert.EqualValues(t, now-50, got.LastSeenAt)
	assert.JSONEq(t, `{"v":"1.0"}`, got.Info)
	assert.NotZero(t, got.CreatedAt) // BeforeCreate populated it

	createdAt := got.CreatedAt

	// upsert (conflict on node_name) updates info/started/last_seen but not created_at
	require.NoError(t, UpsertSystemInstance(node, map[string]any{"v": "2.0"}, now-80, now-10))
	require.NoError(t, DB.Where("node_name = ?", node).First(&got).Error)
	assert.EqualValues(t, now-80, got.StartedAt)
	assert.EqualValues(t, now-10, got.LastSeenAt)
	assert.JSONEq(t, `{"v":"2.0"}`, got.Info)
	assert.Equal(t, createdAt, got.CreatedAt)

	// lastSeenAt == 0 defaults to now
	node2 := uniqNodeName()
	cleanupSystemInstance(t, node2)
	require.NoError(t, UpsertSystemInstance(node2, nil, now, 0))
	// fresh destination: reusing `got` (whose PK is `node`) would make GORM's
	// First add `node_name = node` as an extra condition and miss node2.
	var got2 SystemInstance
	require.NoError(t, DB.Where("node_name = ?", node2).First(&got2).Error)
	assert.GreaterOrEqual(t, got2.LastSeenAt, now)
	assert.Equal(t, "", got2.Info) // nil info -> empty
}

func TestListSystemInstances(t *testing.T) {
	requireDB(t)
	node := uniqNodeName()
	cleanupSystemInstance(t, node)
	now := common.GetTimestamp()
	require.NoError(t, UpsertSystemInstance(node, map[string]any{"v": "x"}, now, now))

	list, err := ListSystemInstances()
	require.NoError(t, err)
	assert.True(t, containsInstance(list, node), "list should contain our instance")
}

func TestDeleteStaleSystemInstance_Scoped(t *testing.T) {
	requireDB(t)
	now := common.GetTimestamp()

	// stale node (last seen well beyond the stale window)
	staleNode := uniqNodeName()
	cleanupSystemInstance(t, staleNode)
	require.NoError(t, UpsertSystemInstance(staleNode, nil, now-1000, now-SystemInstanceStaleAfterSeconds-100))

	// fresh node
	freshNode := uniqNodeName()
	cleanupSystemInstance(t, freshNode)
	require.NoError(t, UpsertSystemInstance(freshNode, nil, now, now))

	// scoped delete removes only the stale node
	deleted, err := DeleteStaleSystemInstance(staleNode, now)
	require.NoError(t, err)
	assert.True(t, deleted)

	var count int64
	DB.Model(&SystemInstance{}).Where("node_name = ?", staleNode).Count(&count)
	assert.EqualValues(t, 0, count)

	// fresh node not stale -> scoped delete is a no-op
	deleted, err = DeleteStaleSystemInstance(freshNode, now)
	require.NoError(t, err)
	assert.False(t, deleted)
	DB.Model(&SystemInstance{}).Where("node_name = ?", freshNode).Count(&count)
	assert.EqualValues(t, 1, count)
}

func TestDeleteStaleSystemInstances_Global(t *testing.T) {
	requireDB(t)
	now := common.GetTimestamp()

	staleNode := uniqNodeName()
	cleanupSystemInstance(t, staleNode)
	require.NoError(t, UpsertSystemInstance(staleNode, nil, now-1000, now-SystemInstanceStaleAfterSeconds-100))

	// global stale sweep removes our stale node (and any other stale rows)
	affected, err := DeleteStaleSystemInstances(now)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, affected, int64(1))

	var count int64
	DB.Model(&SystemInstance{}).Where("node_name = ?", staleNode).Count(&count)
	assert.EqualValues(t, 0, count)
}
