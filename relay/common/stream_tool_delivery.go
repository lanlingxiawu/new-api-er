package common

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// streamResponseToolKey bounds pending response-tool identity independently of ID length.
func streamResponseToolKey(itemID, callID string) string {
	if itemID != "" {
		return fmt.Sprintf("response:item:%x", sha256.Sum256([]byte(itemID)))
	}
	if callID != "" {
		return fmt.Sprintf("response:call:%x", sha256.Sum256([]byte(callID)))
	}
	return "response:complete"
}

// streamToolDeliveryKey 使用定长摘要保存工具别名；避免任意长上游 ID 占用整个连接生命周期的内存。
type streamToolDeliveryKey struct {
	kind   uint8    // 0 为缺失，1 为 item_id/item.id，2 为 call_id，分开两类命名空间。
	digest [32]byte // 标识原始字节的 SHA-256，仅用于当前会话估算去重，不写入日志。
}

// streamToolDeliveries 记录最近 128 个完整有效工具；仅在首次有效且有身份的交付后分配。
type streamToolDeliveries struct {
	seen  map[streamToolDeliveryKey]int // 别名到环形槽位；每槽最多两个别名，始终有界。
	slots [128][2]streamToolDeliveryKey // 两列分别保存 item、call 别名，轮换时一并淘汰。
	next  int                           // 下一次新工具覆盖的槽位；重复事件不推进。
}

// commitResponseToolLocked 合并 Responses/Realtime 两类工具完成事件的计量身份，不影响事件转发。
// 参数 itemID、callID 为可缺失的两类身份，name、args 为本次实际交付的工具名和完整参数。
// 调用者持有会话锁；缺少共同身份时不按内容推测同一调用，非法候选也不占用已计量身份。
func (s *StreamSession) commitResponseToolLocked(itemID, callID, name, args string) {
	var keys [2]streamToolDeliveryKey
	for i, id := range [2]string{itemID, callID} {
		if id != "" {
			keys[i] = streamToolDeliveryKey{kind: uint8(i + 1), digest: sha256.Sum256([]byte(id))}
		}
	}
	if delivered := s.toolDeliveries; delivered != nil {
		for _, key := range keys {
			if key.kind == 0 {
				continue
			}
			if slot, ok := delivered.seen[key]; ok {
				// 后来的另一类完成事件可补齐缺失别名，后续只携带该别名也能命中。
				for i, alias := range keys {
					if alias.kind != 0 && delivered.slots[slot][i].kind == 0 {
						if _, exists := delivered.seen[alias]; !exists {
							delivered.slots[slot][i] = alias
							delivered.seen[alias] = slot
						}
					}
				}
				return
			}
		}
	}
	// 此路径总是完整事件，在本次调用内创建并移除候选；固定 key 不保留上游长 ID。
	key := "response:complete"
	if s.receivedOnly {
		key = streamResponseToolKey(itemID, callID)
		if pending := s.tools[key]; pending != nil {
			// Completion repeats the full arguments; only its unreceived suffix is new.
			if strings.HasPrefix(args, pending.args.String()) {
				args = strings.TrimPrefix(args, pending.args.String())
			} else {
				args = ""
			}
		}
	}
	if !s.toolLocked(key, name, args, true, false) || (itemID == "" && callID == "") {
		return
	}
	if s.toolDeliveries == nil {
		s.toolDeliveries = &streamToolDeliveries{seen: make(map[streamToolDeliveryKey]int)}
	}
	delivered := s.toolDeliveries
	for _, key := range delivered.slots[delivered.next] {
		if key.kind != 0 {
			delete(delivered.seen, key)
		}
	}
	delivered.slots[delivered.next] = keys
	for _, key := range keys {
		if key.kind != 0 {
			delivered.seen[key] = delivered.next
		}
	}
	delivered.next = (delivered.next + 1) % len(delivered.slots)
}
