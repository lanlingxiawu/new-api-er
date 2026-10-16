package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// priceMonitorApplyMaxModels 单次改价的模型数上限。界面一页 20 行，这里只防接口被滥用。
const priceMonitorApplyMaxModels = 100

// priceMonitorApplyFieldOptionKeys 把 API 字段名映射到承载它的 option。
// 与 RATIO_SYNC_FIELDS 对齐并加上 model_price；阶梯表达式（billing_mode / billing_expr）
// 不在其中——行内输入框做不出一个合格的表达式编辑器，见设计文档 §4.4。
var priceMonitorApplyFieldOptionKeys = map[string]string{
	"model_ratio":            "ModelRatio",
	"completion_ratio":       "CompletionRatio",
	"cache_ratio":            "CacheRatio",
	"create_cache_ratio":     "CreateCacheRatio",
	"image_ratio":            "ImageRatio",
	"audio_ratio":            "AudioRatio",
	"audio_completion_ratio": "AudioCompletionRatio",
	"model_price":            "ModelPrice",
}

type priceMonitorApplyPriceRequest struct {
	Changes            []model.ModelPricingChange `json:"changes"`
	ContextVersion     string                     `json:"context_version"`
	ForceBelowFloor    bool                       `json:"force_below_floor"`
	AcknowledgeUnknown bool                       `json:"acknowledge_unknown"`
	CheckedAt          int64                      `json:"checked_at"`
	Items              []any                      `json:"items"`
}

// priceMonitorFloorViolation 描述一个低于保本下限的字段，回传给前端做二次确认。
type priceMonitorFloorViolation struct {
	Model string  `json:"model"`
	Field string  `json:"field"`
	Value float64 `json:"value"`
	Floor float64 `json:"floor"`
	// Binding 是决定这条下限的渠道来源键，便于管理员追因。
	Binding string `json:"binding,omitempty"`
	// Submitted 区分「你填的这个值太低」与「你没改它，但被你改的 input 连带拖到了线下」。
	Submitted bool `json:"submitted"`
}

func ApplyPriceMonitorPrice(c *gin.Context) {
	var request priceMonitorApplyPriceRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if request.Items != nil {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorClientRefreshRequired)
		return
	}
	applyPriceMonitorDrafts(c, request)
}

func priceMonitorFieldNameForOptionKey(optionKey string) string {
	for field, key := range priceMonitorApplyFieldOptionKeys {
		if key == optionKey {
			return field
		}
	}
	return optionKey
}
