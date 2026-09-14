package common

import "bytes"

// StreamFrameScanner 增量识别 SSE 空行；实例仅由所属读取器或写入器使用，不跨请求共享。
type StreamFrameScanner struct {
	offset    int  // 当前尚未消费缓冲已扫描的位置，短读后从这里继续。
	lineStart int  // 当前行正文起点，用于区分字段行与空行。
	skipLF    bool // 前一次扫描以 CR 结束；紧随的 LF 属于同一个换行，不形成额外空行。
}

// End 扫描当前未消费缓冲 data，返回首个完整帧或上一帧 CR 后延续 LF 的结束偏移；0 表示继续补充字节。
// 正数返回后调用者须移除对应前缀；CR 空行立即完成，延续 LF 单独返回原字节但不代表新事件。
func (s *StreamFrameScanner) End(data []byte) int {
	for s.offset < len(data) {
		if s.skipLF {
			s.skipLF = false
			if data[s.offset] == '\n' {
				if s.offset == 0 {
					// 上帧以 CR 完成：独立交付其后续 LF 原字节，不误作下一帧的空行。
					return 1
				}
				s.offset++
				s.lineStart = s.offset
				continue
			}
		}
		relative := bytes.IndexAny(data[s.offset:], "\r\n")
		if relative < 0 {
			s.offset = len(data)
			return 0
		}
		i := s.offset + relative
		empty := i == s.lineStart
		s.offset = i + 1
		if data[i] == '\r' {
			if s.offset < len(data) && data[s.offset] == '\n' {
				s.offset++
			} else if s.offset == len(data) {
				s.skipLF = true
			}
		}
		s.lineStart = s.offset
		if empty {
			end := s.offset
			s.offset, s.lineStart = 0, 0
			return end
		}
	}
	return 0
}

// StreamFrameEnd 检查独立缓冲 data 的首个 SSE 空行边界，支持 LF/CRLF/CR 混用；未找到返回 0。
// 持续短读的调用者使用 StreamFrameScanner，避免反复扫描旧前缀。
func StreamFrameEnd(data []byte) int {
	var scanner StreamFrameScanner
	return scanner.End(data)
}

// StreamFramePayload 从原始帧 raw 提取事件名 event 和多行 data；保留原始字节，智谱 finish 优先采用 meta。
func StreamFramePayload(raw []byte) (event string, data []byte) {
	var lines [][]byte
	var meta []byte
	for len(raw) > 0 {
		line := raw
		if i := bytes.IndexAny(raw, "\r\n"); i >= 0 {
			line = raw[:i]
			end := i + 1
			if raw[i] == '\r' && end < len(raw) && raw[end] == '\n' {
				end++
			}
			raw = raw[end:]
		} else {
			raw = nil
		}
		key, value, _ := bytes.Cut(line, []byte{':'})
		value = bytes.TrimPrefix(value, []byte{' '})
		switch string(key) {
		case "event":
			event = string(value)
		case "data":
			lines = append(lines, value)
		case "meta":
			meta = value
		}
	}
	if event == "finish" && len(meta) > 0 {
		return event, meta
	}
	return event, bytes.Join(lines, []byte{'\n'})
}
