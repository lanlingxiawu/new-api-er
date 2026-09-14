package zhipu

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// managedZhipuStream 处理旧智谱 add/finish/meta 协议，替代旧的无界读取协程；非流式不进入本方法。
// 参数 resp 为原始响应，info 保存确认用量与交付状态；返回最终用量，不在此处结算资金。
// 参数 c：下游 SSE 写入和请求生命周期上下文。
func managedZhipuStream(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	usage := &dto.Usage{}
	helper.StreamEventScannerHandler(c, resp, info, func(event, data string, sr *helper.StreamResult) {
		if event == "finish" {
			var meta ZhipuStreamMetaResponse
			if err := common.UnmarshalJsonStr(data, &meta); err != nil {
				sr.Stop(err)
				return
			}
			response, u := streamMetaResponseZhipu2OpenAI(&meta)
			usage = u
			if err := helper.ObjectData(c, response); err != nil {
				sr.Stop(err)
			}
			sr.Done()
			return
		}
		if err := helper.ObjectData(c, streamResponseZhipu2OpenAI(data)); err != nil {
			sr.Stop(err)
		}
	})
	helper.Done(c)
	return usage, nil
}
