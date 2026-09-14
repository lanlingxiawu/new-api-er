package common

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/tidwall/gjson"
)

// UseStreamImageTaskResponse 在开始读取 resp 前声明 Ali 异步图片任务阶段；其他渠道、非图片或旧流程响应跳过。
// 只改变本次响应的中间态校验规则，不放宽 JSON、读取错误或最终图片完整性检查。
func UseStreamImageTaskResponse(resp *http.Response) {
	if resp == nil {
		return
	}
	if body, ok := resp.Body.(*streamObservedBody); ok && body.session.ChannelType == constant.ChannelTypeAli && body.session.ExpectedImages > 0 {
		body.imageTask = true
	}
}

// observeImageJSON 按渠道原生结构验证已通过语法/用量检查的 v；imageTask 标记 Ali 任务响应。
// 仅合法任务中间态返回 true；最终图片足量时更新确认数量并完成，否则记录上游异常。
func (s *StreamSession) observeImageJSON(v gjson.Result, imageTask bool) bool {
	count, valid := 0, true
	switch s.ChannelType {
	case constant.ChannelTypeMiniMax:
		for _, path := range []string{"data.image_urls", "data.image_base64"} {
			images := v.Get(path)
			if !images.Exists() || images.Type == gjson.Null {
				continue
			}
			if !images.IsArray() {
				valid = false
				break
			}
			images.ForEach(func(_, item gjson.Result) bool {
				valid = item.Type == gjson.String && strings.TrimSpace(item.Str) != ""
				if valid {
					count++
				}
				return valid
			})
			if !valid {
				break
			}
		}
	case constant.ChannelTypeAli:
		status := v.Get("output.task_status").String()
		if v.Get("code").String() != "" || v.Get("message").String() != "" || v.Get("output.code").String() != "" || status == "FAILED" || status == "CANCELED" || status == "UNKNOWN" {
			s.Fail("upstream_error", errors.New("upstream image task failed"))
			return false
		}
		if imageTask {
			taskID := v.Get("output.task_id")
			if (status == "PENDING" || status == "RUNNING") && taskID.Type == gjson.String && strings.TrimSpace(taskID.Str) != "" {
				return true // 一次任务查询结束，不代表已获得图片，也不产生 image_count。
			}
			valid = status == "SUCCEEDED"
		} else {
			valid = status == "" || status == "SUCCEEDED"
		}
		results := v.Get("output.results")
		if results.IsArray() && len(results.Array()) > 0 {
			var itemsValid bool
			count, itemsValid = streamImageItemCount(results, "b64_image")
			valid = valid && itemsValid
		} else {
			choices := v.Get("output.choices")
			valid = valid && choices.IsArray()
			choices.ForEach(func(_, choice gjson.Result) bool {
				contents, hasImage := choice.Get("message.content"), false
				valid = valid && contents.IsArray()
				contents.ForEach(func(_, content gjson.Result) bool {
					img := content.Get("image")
					valid = valid && content.IsObject() && (!img.Exists() || img.Type == gjson.Null || img.Type == gjson.String)
					if img.Type == gjson.String && strings.TrimSpace(img.Str) != "" {
						hasImage = true
					}
					return valid
				})
				valid = valid && hasImage
				if valid {
					count++ // 既有转换器每个 choice 输出一张，多个 image 内容不重复计数。
				}
				return valid
			})
		}
	default:
		count, valid = streamImageItemCount(v.Get("data"), "b64_json")
	}
	if !valid || count < s.ExpectedImages {
		s.Fail("upstream_protocol_error", errors.New("upstream JSON lacks complete image results"))
		return false
	}
	s.mu.Lock()
	if s.state.Reason == "" {
		s.state.Evidence["image_count"] = count // 原生实际结果数量，不重复累计转换后的输出。
	}
	s.mu.Unlock()
	s.Complete()
	return false
}

// streamImageItemCount 检查 images 数组内每项的 URL 或 blobField 字符串；返回有效项数与全体是否完整，不解码图片。
func streamImageItemCount(images gjson.Result, blobField string) (int, bool) {
	count, valid := 0, images.IsArray()
	if !valid {
		return 0, false
	}
	images.ForEach(func(_, item gjson.Result) bool {
		blob, url := item.Get(blobField), item.Get("url")
		valid = item.IsObject() && (blob.Type == gjson.String && strings.TrimSpace(blob.Str) != "" || url.Type == gjson.String && strings.TrimSpace(url.Str) != "")
		if valid {
			count++
		}
		return valid
	})
	return count, valid
}
