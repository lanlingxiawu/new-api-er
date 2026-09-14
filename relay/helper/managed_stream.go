package helper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// managedStreamRead 是单帧有界通道的结果；raw 和 err 保持读取顺序，避免 EOF 抢先丢掉队列尾帧。
type managedStreamRead struct {
	raw []byte // 已读取的一帧 SSE 或一行 NDJSON，供所有者执行转换。
	err error  // 排在已读帧之后的读取终止原因，可为 EOF。
}

// managedStreamScannerHandler 为通用流式提供单写入所有者及一个有界读取者。
// 参数 c 为下游上下文，resp 为原始响应，info 为活动会话，callback 接收 data 与可停止的转换结果；事件名在此接口中丢弃。
// 协议和用量由写入所有者逐帧观察；无返回值，终止错误保存在会话，退出前关闭响应并等待读者完成。
func managedStreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, callback func(string, *StreamResult)) {
	StreamEventScannerHandler(c, resp, info, func(_ string, data string, sr *StreamResult) { callback(data, sr) })
}

// StreamEventScannerHandler 保留事件名以支持扩展 SSE；callback 在单个写入所有者上执行。
// 参数 c 为下游上下文，resp 为上游响应，info 为活动会话；callback 依次接收事件名、data 及转换结果，无返回值。
func StreamEventScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, callback func(string, string, *StreamResult)) {
	managedProtocolScanner(c, resp, info, false, callback)
}

// StreamLineScannerHandler 保留 NDJSON 行语义，使用与 SSE 相同的取消、短写、读错误和终止所有者。
// 参数 c 为下游上下文，resp 为上游响应，info 为活动会话；callback 接收原始 JSON 行及转换结果，无返回值。
func StreamLineScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, callback func(string, *StreamResult)) {
	managedProtocolScanner(c, resp, info, true, func(_ string, data string, sr *StreamResult) { callback(data, sr) })
}

// managedProtocolScanner 串行消费有界读取结果；lines 选择 NDJSON，否则保留完整 SSE 帧。
// 参数 c 为请求/写入上下文，resp 为有非 nil Body 的上游响应，info 为活动会话；callback 在所有者上接收事件名、data 和转换结果。
// 无返回值，错误与取消由会话/请求保存；定时 ping 也由同一所有者写出，退出时关闭上游并等待读者。
func managedProtocolScanner(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, lines bool, callback func(string, string, *StreamResult)) {
	if resp.Body == nil {
		info.StreamSession.Fail("upstream_read_error", errors.New("missing upstream body"))
		return
	}
	if !info.StreamSession.Snapshot().HTTPObserved {
		info.StreamDiagnostic.Observe(resp)
		info.StreamSession.ObserveHTTP(resp)
	}
	// 原始采集留在底层，只移除会提前推进协议状态的 Read 包装；读协程仅分帧。
	if body, ok := resp.Body.(interface{ UnwrapStream() io.ReadCloser }); ok {
		resp.Body = body.UnwrapStream()
	}
	info.StreamSession.BindUpstream(resp.Body)
	info.StreamStatus = relaycommon.NewStreamStatus()
	SetEventStreamHeaders(c)
	copyCodexSSEHeaders(c, resp)
	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := make(chan managedStreamRead, 1)
	finished := make(chan struct{})
	common.RelayCtxGo(stop, func() {
		defer close(finished)
		defer close(reads)
		defer func() {
			if r := recover(); r != nil {
				select {
				case reads <- managedStreamRead{err: fmt.Errorf("stream reader panic: %v", r)}:
				case <-stop.Done():
				}
			}
		}()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), relaycommon.MaxStreamFrameBytes)
		var framing relaycommon.StreamFrameScanner // 单读取者独占，跨短读只扫描新增字节。
		scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if lines {
				return bufio.ScanLines(data, atEOF)
			}
			if end := framing.End(data); end > 0 {
				return end, data[:end], nil
			}
			if atEOF && len(strings.TrimSpace(string(data))) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		for scanner.Scan() {
			raw := append([]byte(nil), scanner.Bytes()...)
			select {
			case reads <- managedStreamRead{raw: raw}:
			case <-stop.Done():
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case reads <- managedStreamRead{err: err}:
		case <-stop.Done():
		}
	})
	defer func() { cancel(); _ = resp.Body.Close(); <-finished }()
	var idle *time.Timer
	var idleC <-chan time.Time
	idleDuration := time.Duration(constant.StreamingTimeout) * time.Second
	if !service.IsRelayTimeoutManaged(c) && idleDuration > 0 {
		idle = time.NewTimer(idleDuration)
		idleC = idle.C
		defer idle.Stop()
	}
	settings := operation_setting.GetGeneralSetting()
	var ping *time.Ticker
	var pingC <-chan time.Time
	if settings.PingIntervalEnabled && !info.DisablePing {
		interval := time.Duration(settings.PingIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = DefaultPingInterval
		}
		ping = time.NewTicker(interval)
		pingC = ping.C
		defer ping.Stop()
	}
	sr := newStreamResult(info.StreamStatus)
	sr.session = info.StreamSession
	defer func() {
		if r := recover(); r != nil {
			// 本地转换 panic 保留兼容终止标签，但不冒充已确认的上游协议异常。
			info.StreamSession.FailRelay("upstream_protocol_error", fmt.Errorf("stream conversion panic: %v", r))
		}
	}()
	for {
		if c.Request.Context().Err() != nil {
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-idleC:
			info.StreamSession.Fail(relaycommon.StreamEndReasonTimeout, context.DeadlineExceeded)
			return
		case <-pingC:
			ExtendWriteDeadline(c) // 保活也续期，避免沿用上次正文写入的过期期限。
			if err := PingData(c); err != nil {
				info.StreamSession.ClientFailed(err)
				return
			}
		case frame, ok := <-reads:
			if !ok {
				return
			}
			if frame.err != nil {
				info.StreamSession.EndRead(frame.err)
				return
			}
			if idle != nil {
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(idleDuration)
			}
			event, data := relaycommon.StreamFramePayload(frame.raw)
			if lines {
				event = ""
				data = frame.raw
				if err := info.StreamSession.ObserveEvent(event, data); err != nil {
					return
				}
			} else if err := info.StreamSession.ObserveFrame(frame.raw); err != nil {
				return
			}
			if relaycommon.IsStreamErrorEvent(event, data) {
				return
			}
			if strings.TrimSpace(string(data)) == "[DONE]" {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				return
			}
			if len(data) == 0 {
				continue
			}
			if gjson.GetBytes(data, "type").String() != "ping" {
				info.SetFirstResponseTime()
			}
			info.ReceivedResponseCount++
			ExtendWriteDeadline(c)
			sr.reset()
			callback(event, string(data), sr)
			if sr.IsStopped() {
				return
			}
			if info.StreamSession.ClientError() != nil {
				return
			}
		}
	}
}
