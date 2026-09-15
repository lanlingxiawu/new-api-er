package controller

import (
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

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

type priceMonitorApplyPriceItem struct {
	Model    string              `json:"model"`
	Fields   map[string]*float64 `json:"fields"`
	Expected map[string]*float64 `json:"expected"`
}

type priceMonitorApplyPriceRequest struct {
	CheckedAt      int64                        `json:"checked_at"`
	PricingVersion int64                        `json:"pricing_version"`
	Items          []priceMonitorApplyPriceItem `json:"items"`
	// Force 允许管理员在明知低于保本下限时仍然提交（亏损引流等刻意定价）。
	// 默认 false：服务端必须自证安全，不能只靠前端提示——直接调接口即可绕过。
	Force bool `json:"force"`
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

type priceMonitorApplyPriceResult struct {
	Model     string   `json:"model"`
	Applied   []string `json:"applied"`
	Unchanged []string `json:"unchanged"`
}

// ApplyPriceMonitorPrice 按模型局部改写平台价格。整个请求是一个事务：要么全部生效，
// 要么一个字段都不落，避免出现「改了 ModelRatio 但 CompletionRatio 没改」的半成品价格。
func ApplyPriceMonitorPrice(c *gin.Context) {
	var request priceMonitorApplyPriceRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(request.Items) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(request.Items) > priceMonitorApplyMaxModels {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorTooManyModels)
		return
	}

	snapshot := getPriceMonitorStore().Get()
	if request.CheckedAt != snapshot.CheckedAt {
		common.ApiErrorI18n(c, i18n.MsgPriceMonitorSnapshotStale)
		return
	}
	snapshotModels := make(map[string]PriceMonitorPriceCell, len(snapshot.MatrixItems))
	snapshotFloors := make(map[string]*PriceMonitorRepairFloor, len(snapshot.MatrixItems))
	for _, item := range snapshot.MatrixItems {
		if platform, ok := item.Prices[priceMonitorPlatformKey]; ok {
			snapshotModels[item.Model] = platform
		}
		if item.RepairFloor != nil {
			snapshotFloors[item.Model] = item.RepairFloor
		}
	}
	var violations []priceMonitorFloorViolation

	patches := make([]model.PricingPatch, 0, len(request.Items)*2)
	models := make([]string, 0, len(request.Items))
	seen := make(map[string]struct{}, len(request.Items))
	for _, item := range request.Items {
		modelName := strings.TrimSpace(item.Model)
		if modelName == "" || len(item.Fields) == 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		if _, duplicate := seen[modelName]; duplicate {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		seen[modelName] = struct{}{}
		platform, known := snapshotModels[modelName]
		if !known {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		if platform.Mode == priceMonitorModeExpression || platform.Dynamic {
			common.ApiErrorI18n(c, i18n.MsgPriceMonitorExprNotEditable)
			return
		}
		for field, value := range item.Fields {
			optionKey, supported := priceMonitorApplyFieldOptionKeys[field]
			// 不接受删除（null）：删掉覆盖后回退到哪个值取决于各 getter 的内置规则，这里没法自证安全。
			if !supported || value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				return
			}
			// 字段必须与平台计费模式一致：按量模型写 model_price 会把它悄悄切成按次计费（且不受
			// 保本下限约束），按次模型写比值字段则根本不生效。
			if (platform.Mode == priceMonitorModeRequest) != (field == "model_price") {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				return
			}
			// 补全倍率被系统硬编码锁定的模型，计费根本不读 CompletionRatio 里的值：写进去只会让
			// 接口报告「已应用」而实际售价纹丝不动。直接拒绝，并告诉管理员该改哪一项。
			if field == "completion_ratio" {
				if info := ratio_setting.GetCompletionRatioInfo(modelName); info.Locked {
					common.ApiErrorI18n(c, i18n.MsgPriceMonitorCompletionRatioLocked, map[string]any{
						"Model": modelName,
						"Ratio": strconv.FormatFloat(info.Ratio, 'f', -1, 64),
					})
					return
				}
			}
			patches = append(patches, model.PricingPatch{
				OptionKey: optionKey,
				Model:     modelName,
				Expected:  item.Expected[field],
				Value:     value,
			})
		}
		// 保本下限复算。必须在**合并后的完整价格**上判，而不是只看本次提交的字段：
		// 比值型字段（completion_ratio / lane 类）的实际售价 = input × ratio，
		// 只把 input 从 20 改成 2、不动 completion_ratio，输出价照样从 20 掉到 2，
		// 但因为 completion_ratio 没出现在 item.Fields 里就被整个跳过了。
		violations = append(violations,
			priceMonitorFloorViolations(snapshotFloors[modelName], modelName, item.Fields)...)
		models = append(models, modelName)
	}

	// 一个字段不达标就整单拒绝，不做部分写入——半成品价格比不改更危险。
	if len(violations) > 0 && !request.Force {
		sort.Slice(violations, func(i, j int) bool {
			if violations[i].Model != violations[j].Model {
				return violations[i].Model < violations[j].Model
			}
			return violations[i].Field < violations[j].Field
		})
		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"message":    common.TranslateMessage(c, i18n.MsgPriceMonitorBelowFloor),
			"data":       gin.H{"violations": violations},
			"error_code": "PRICE_BELOW_FLOOR",
		})
		return
	}

	version, applied, err := model.PatchPricingOptions(request.PricingVersion, patches)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrPricingVersionConflict), errors.Is(err, model.ErrPricingValueConflict):
			common.ApiErrorI18n(c, i18n.MsgPricingConfigChanged)
		default:
			logger.LogError(c, "price monitor apply price failed: "+err.Error())
			common.ApiErrorI18n(c, i18n.MsgRetryLater)
		}
		return
	}

	auditMeta := map[string]interface{}{
		"models": models,
		"fields": priceMonitorAppliedFieldNames(applied),
	}
	if len(violations) > 0 {
		// 突破下限是需要留痕的动作。
		auditMeta["forced"] = true
		auditMeta["below_floor"] = violations
	}
	recordManageAudit(c, "price_monitor.price.apply", auditMeta)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"pricing_version": version,
			"results":         priceMonitorApplyResults(request.Items, applied),
		},
	})
}

// priceMonitorApplyResults 把「实际写入的 patch」翻回按模型分组的字段名，
// 未写入的字段归入 unchanged，让前端能如实告诉管理员哪些真的改了。
func priceMonitorApplyResults(items []priceMonitorApplyPriceItem, applied map[string][]model.PricingPatch) []priceMonitorApplyPriceResult {
	appliedByModel := make(map[string]map[string]struct{})
	for optionKey, patches := range applied {
		for _, patch := range patches {
			fields, ok := appliedByModel[patch.Model]
			if !ok {
				fields = make(map[string]struct{})
				appliedByModel[patch.Model] = fields
			}
			fields[priceMonitorFieldNameForOptionKey(optionKey)] = struct{}{}
		}
	}
	results := make([]priceMonitorApplyPriceResult, 0, len(items))
	for _, item := range items {
		modelName := strings.TrimSpace(item.Model)
		result := priceMonitorApplyPriceResult{Model: modelName, Applied: []string{}, Unchanged: []string{}}
		for field := range item.Fields {
			if _, ok := appliedByModel[modelName][field]; ok {
				result.Applied = append(result.Applied, field)
			} else {
				result.Unchanged = append(result.Unchanged, field)
			}
		}
		sort.Strings(result.Applied)
		sort.Strings(result.Unchanged)
		results = append(results, result)
	}
	return results
}

func priceMonitorFieldNameForOptionKey(optionKey string) string {
	for field, key := range priceMonitorApplyFieldOptionKeys {
		if key == optionKey {
			return field
		}
	}
	return optionKey
}

func priceMonitorAppliedFieldNames(applied map[string][]model.PricingPatch) []string {
	names := make([]string, 0, len(applied))
	for optionKey := range applied {
		names = append(names, priceMonitorFieldNameForOptionKey(optionKey))
	}
	sort.Strings(names)
	return names
}

// priceMonitorFloorViolations 在**合并后的完整生效价格**上复算保本下限。
//
// 两个必须避开的坑：
//
//  1. 不能只检查本次提交的字段。比值型字段的实际售价是 input × ratio，
//     把 input 从 20 降到 2 而不动 completion_ratio(=1)，输出价同样从 20 掉到 2；
//     如果 completion_ratio 没出现在提交里就跳过，这次改价会带着一个远低于保本线的
//     输出价通过校验。所以要遍历 floor.Fields，缺的字段用**当前生效值**补齐。
//
//  2. 补齐时不能用巡检快照里的价格。快照可能是几小时前的，期间平台价已被改过；
//     客户端只要取到最新的 pricing_version，版本校验就拦不住它，于是拿旧 input
//     算出的比值门槛偏松，写进一个实际亏损的价格。所以读 ratio_setting 的**当前**值。
func priceMonitorFloorViolations(
	floor *PriceMonitorRepairFloor,
	modelName string,
	submitted map[string]*float64,
) []priceMonitorFloorViolation {
	if floor == nil || len(floor.Fields) == 0 {
		return nil
	}
	// 生效后的展示 input（每百万 token）：input = model_ratio * 2。
	effectiveInput, hasInput := priceMonitorEffectiveInput(modelName, submitted)

	var violations []priceMonitorFloorViolation
	for field, base := range floor.Fields {
		value, known := priceMonitorEffectiveFieldValue(modelName, field, submitted)
		if !known {
			// 既没提交、当前也读不到值，无从判断。
			continue
		}
		limit := base
		if field != "model_ratio" && field != "model_price" {
			display, hasDisplay := floor.Display[field]
			if !hasDisplay {
				continue
			}
			if !hasInput || effectiveInput <= 0 {
				continue
			}
			limit = display / effectiveInput
		}
		if value >= limit || nearlyEqual(value, limit) {
			continue
		}
		violations = append(violations, priceMonitorFloorViolation{
			Model: modelName, Field: field, Value: value, Floor: limit,
			Binding:   floor.Binding[field],
			Submitted: priceMonitorFieldSubmitted(submitted, field),
		})
	}
	return violations
}

// priceMonitorFieldSubmitted 报告该字段是否由本次请求直接改动（而不是被改动的 input 连带拖下去）。
func priceMonitorFieldSubmitted(submitted map[string]*float64, field string) bool {
	_, ok := submitted[field]
	return ok
}

// priceMonitorEffectiveFieldValue 求某字段生效后的值：提交了就用提交值，否则用当前生效值。
func priceMonitorEffectiveFieldValue(modelName, field string, submitted map[string]*float64) (float64, bool) {
	if value, ok := submitted[field]; ok && value != nil {
		return *value, true
	}
	return priceMonitorCurrentFieldValue(modelName, field)
}

// priceMonitorCurrentFieldValue 读取某个改价字段**当前生效**的值。
//
// 走 ratio_setting 的 getter 而不是直接读 option 表：getter 会回退到内存默认值，
// 与计费实际使用的值一致；直接读表会把「尚未持久化的默认价」当成未配置。
func priceMonitorCurrentFieldValue(modelName, field string) (float64, bool) {
	switch field {
	case "model_ratio":
		ratio, ok, _ := ratio_setting.GetModelRatio(modelName)
		return ratio, ok
	case "completion_ratio":
		return ratio_setting.GetCompletionRatio(modelName), true
	case "model_price":
		return ratio_setting.GetModelPrice(modelName, false)
	case "cache_ratio":
		return ratio_setting.GetCacheRatio(modelName)
	case "create_cache_ratio":
		return ratio_setting.GetCreateCacheRatio(modelName)
	case "image_ratio":
		return ratio_setting.GetImageRatio(modelName)
	case "audio_ratio":
		return ratio_setting.GetAudioRatio(modelName), true
	case "audio_completion_ratio":
		return ratio_setting.GetAudioCompletionRatio(modelName), true
	}
	return 0, false
}

// priceMonitorEffectiveInput 求本次提交生效后的展示 input（每百万 token）。
// 与 priceMonitorEffectiveFieldValue 同一口径：没提交 model_ratio 时用**当前生效**的倍率
// （不用巡检快照里的旧值）。
func priceMonitorEffectiveInput(modelName string, submitted map[string]*float64) (float64, bool) {
	ratio, ok := priceMonitorEffectiveFieldValue(modelName, "model_ratio", submitted)
	if !ok {
		return 0, false
	}
	return ratio * 2, true
}
