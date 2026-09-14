package helper

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// StreamResult 供逐帧转换回调记录错误或结束状态；扫描器在回调返回后检查停止标记。
// 旧扫描路径保留软错误语义，受管路径的解析或转换错误会停止后续处理。
type StreamResult struct {
	status  *relaycommon.StreamStatus
	stopped bool
	session *relaycommon.StreamSession // 通用流式开启后解析及转换错误需终止，避免异常后补造成功。
}

func newStreamResult(status *relaycommon.StreamStatus) *StreamResult {
	return &StreamResult{status: status}
}

// Error 接收解析错误 err；旧路径继续软错误语义，受管流记录首个错误并停止，不把底层原因作为公开摘要。
func (r *StreamResult) Error(err error) {
	if err == nil {
		return
	}
	r.status.RecordError(err.Error())
	if r.session.Active() {
		r.session.Fail("upstream_json_error", err)
		r.stopped = true
	}
}

// ConversionError 接收本地转换或输出错误 err；受管路径停止回调并登记本地原因，旧路径仍只记录软错误。
// 写入器已登记的客户端失败或更早的上游原因由会话保留，不被此入口覆盖；nil 不改变状态。
func (r *StreamResult) ConversionError(err error) {
	if err == nil {
		return
	}
	r.status.RecordError(err.Error())
	if r.session.Active() {
		r.session.Fail("response_conversion_error", err)
		r.stopped = true
	}
}

// Stop 接收终止错误 err；受管会话保存错误供唯一补发/结算，nil 表示仅停止本次转换。
func (r *StreamResult) Stop(err error) {
	if err != nil {
		r.status.RecordError(err.Error())
		if r.session.Active() {
			r.session.Fail("upstream_protocol_error", err)
		}
	}
	r.status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, err)
	r.stopped = true
}

// Done signals that the handler has finished processing normally
// (e.g., Dify "message_end"). The stream stops after this chunk.
func (r *StreamResult) Done() {
	r.status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	r.stopped = true
}

// IsStopped returns whether Stop() or Done() was called during this chunk.
func (r *StreamResult) IsStopped() bool {
	return r.stopped
}

// reset clears the per-chunk stopped flag so the object can be reused.
func (r *StreamResult) reset() {
	r.stopped = false
}
