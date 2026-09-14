package common

import "strings"

// StreamMediaType 提取响应头 contentType 的基础 MIME 类型并转为小写，供受管流式各层一致分类。
// 只忽略分号后的参数和类型首尾空白，不解析参数 map、不修改原始头；空输入返回空字符串。
func StreamMediaType(contentType string) string {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(mediaType))
}

// IsStreamBinaryContentType 判断原始响应头 contentType 是否为受支持的裸媒体，参数值和大小写不参与判断。
// 观察器、音频转发与终止错误共用此规则，避免媒体误按文本拆帧或被追加 SSE 错误。
func IsStreamBinaryContentType(contentType string) bool {
	mediaType := StreamMediaType(contentType)
	return strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/") || mediaType == "application/octet-stream"
}
