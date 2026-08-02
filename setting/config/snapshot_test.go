package config

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type probeSetting struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Items []string `json:"items"`
}

func TestSnapshotLoadReturnsPublishedValue(t *testing.T) {
	var snapshot Snapshot[probeSetting]
	require.Nil(t, snapshot.Load(), "未发布前应当是 nil，暴露漏发布而不是给出零值假象")

	snapshot.Publish(probeSetting{Name: "a", Count: 1})
	require.Equal(t, "a", snapshot.Load().Name)

	snapshot.Publish(probeSetting{Name: "b", Count: 2})
	require.Equal(t, "b", snapshot.Load().Name)
}

// 已经拿到手的快照不能被后续发布改写——这正是它存在的意义。
func TestSnapshotHeldPointerIsImmutable(t *testing.T) {
	var snapshot Snapshot[probeSetting]
	snapshot.Publish(probeSetting{Name: "old", Items: []string{"x"}})

	held := snapshot.Load()
	snapshot.Publish(probeSetting{Name: "new", Items: []string{"y", "z"}})

	assert.Equal(t, "old", held.Name)
	assert.Equal(t, []string{"x"}, held.Items)
	assert.Equal(t, "new", snapshot.Load().Name)
}

// TestSnapshotSurvivesConcurrentPublishAndLoad 是这套机制要解决的问题本身：
// 读侧不能观察到"写了一半"的结构体。每次发布让所有字段携带同一个身份值，
// 读侧只要发现字段间身份不一致就说明发生了撕裂。
func TestSnapshotSurvivesConcurrentPublishAndLoad(t *testing.T) {
	var snapshot Snapshot[probeSetting]
	// 初始值也必须满足下面的身份不变式（Name[0]-'a' == Count），
	// 否则读侧在第一次发布之前观察到它会被误判成撕裂。
	snapshot.Publish(probeSetting{Name: "a", Count: 0, Items: []string{"a"}})

	const writers, readers, rounds = 4, 4, 2000
	var writersWG, readersWG sync.WaitGroup
	stop := make(chan struct{})
	var torn atomic.Int64

	for w := 1; w <= writers; w++ {
		writersWG.Add(1)
		go func(seed int) {
			defer writersWG.Done()
			identity := string(rune('a' + seed))
			for i := 0; i < rounds; i++ {
				snapshot.Publish(probeSetting{
					Name: identity, Count: seed, Items: []string{identity},
				})
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				observed := snapshot.Load()
				if observed == nil {
					continue
				}
				if len(observed.Items) != 1 || observed.Items[0] != observed.Name ||
					observed.Count != int(observed.Name[0]-'a') {
					torn.Add(1)
					return
				}
			}
		}()
	}

	writersWG.Wait()
	close(stop)
	readersWG.Wait()

	assert.Zero(t, torn.Load(), "读到了写了一半的结构体")
}

// 注册即发布：进程起来后立刻有可读快照，不必等第一次配置变更。
func TestRegisterSnapshotPublishesImmediately(t *testing.T) {
	manager := NewConfigManager()
	draft := probeSetting{Name: "initial", Count: 7}
	var snapshot Snapshot[probeSetting]

	manager.RegisterSnapshot("probe_setting_initial", &draft, func() { snapshot.Publish(draft) })

	require.NotNil(t, snapshot.Load(), "注册之后就该有快照")
	assert.Equal(t, "initial", snapshot.Load().Name)
	assert.Equal(t, 7, snapshot.Load().Count)
}

// 配置改动之后快照必须自动重发，否则读侧永远停在旧值。
func TestUpdateFromMapRepublishesSnapshot(t *testing.T) {
	manager := NewConfigManager()
	draft := probeSetting{Name: "before", Count: 1}
	var snapshot Snapshot[probeSetting]
	manager.RegisterSnapshot("probe_setting_update", &draft, func() { snapshot.Publish(draft) })

	require.NoError(t, manager.UpdateFromMap("probe_setting_update", map[string]string{
		"name":  "after",
		"count": "42",
	}))

	assert.Equal(t, "after", snapshot.Load().Name, "配置改了但快照没重发")
	assert.Equal(t, 42, snapshot.Load().Count)
}

// 未注册快照的模块走原路径，不能因为回调缺失而 panic。
func TestUpdateFromMapWithoutSnapshotIsSafe(t *testing.T) {
	manager := NewConfigManager()
	draft := probeSetting{Name: "plain"}
	manager.Register("probe_setting_plain", &draft)

	require.NotPanics(t, func() {
		_ = manager.UpdateFromMap("probe_setting_plain", map[string]string{"name": "changed"})
	})
	assert.Equal(t, "changed", draft.Name)
}
