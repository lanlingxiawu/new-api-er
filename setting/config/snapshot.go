package config

import "sync/atomic"

// Snapshot 给配置模块提供"读不加锁、写整体替换"的稳定视图。
//
// 背景：注册进 ConfigManager 的是**结构体指针**，LoadFromDB / SaveToDB /
// ExportAllConfigs / UpdateFromMap 都会在 configDraftMutex 下用反射原地改这些字段。
// 而传统的 GetXxxSetting() 直接把裸指针交出去，调用方读字段时不持任何锁：
//
//   - string / slice / map 字段是多字长的，原地改写时读侧可能拿到半个值 ——
//     这是能真正读出非法数据的竞争，不只是理论问题；
//   - int / bool 字段在受支持的架构上单字读写是原子的，实际读不出坏值，
//     但仍违反 Go 内存模型，`go test -race` 会报警。
//
// Snapshot 把读侧改成读一份不可变副本：写侧在临界区内 Publish 一次，
// 读侧 Load 到的指针在其整个生命周期内都不会再被改写。
//
// 用法见 general_setting.go / fetch_setting.go；设计依据是
// docs/design/env-hot-config-migration.md §16.6 的方案 A。
type Snapshot[T any] struct {
	value atomic.Pointer[T]
}

// Publish 发布一份新的不可变副本。
//
// 调用方必须已经持有配置草稿锁（也就是只能从 RegisterSnapshot 注册的发布函数、
// 或 WithConfigDraft 内部调用），否则读草稿这一步本身就是竞争。
func (s *Snapshot[T]) Publish(value T) {
	s.value.Store(&value)
}

// Load 返回当前快照。返回的指针指向不可变副本，可以安全地长期持有并读取，
// 但**不要**通过它写入——写入不会生效，也不会被其他读者看到。
func (s *Snapshot[T]) Load() *T {
	return s.value.Load()
}

// snapshotPublishers 保存每个模块在草稿被改写后需要执行的重新发布动作。
var snapshotPublishers = map[string]func(){}

// RegisterSnapshot 在注册配置模块的同时登记它的快照发布函数。
//
// UpdateFromMap 写完草稿之后会在同一个临界区内回调它，所以：
//   - 发布函数内部**不得**再次获取草稿锁（会自死锁）；
//   - 发布函数直接读包级草稿变量即可，此时锁已在手。
func (cm *ConfigManager) RegisterSnapshot(name string, config interface{}, publish func()) {
	cm.Register(name, config)
	configDraftMutex.Lock()
	snapshotPublishers[name] = publish
	configDraftMutex.Unlock()
	// 注册即发布一次，保证进程启动后立刻有可读快照，
	// 而不是等到第一次配置变更。
	WithConfigDraft(publish)
}

// republishSnapshotLocked 在草稿锁内重新发布指定模块的快照。
func republishSnapshotLocked(name string) {
	if publish := snapshotPublishers[name]; publish != nil {
		publish()
	}
}
