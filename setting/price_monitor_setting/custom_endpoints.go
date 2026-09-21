package price_monitor_setting

import (
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"strings"
)

const (
	// maxCustomEndpointEntries 与 maxCustomEndpointLength 限制配置体积：这份配置整体以
	// 一行 JSON 存进 options 表，并在每轮巡检里被读取，不能让它无限增长。
	maxCustomEndpointEntries = 200
	maxCustomEndpointLength  = 512
)

// SanitizeEndpoint 归一化一条价格接口配置。
//
// 取值范围与同步上游倍率弹窗一致：相对路径（/api/pricing、/api/ratio_config）或
// http(s) 绝对地址。绝对地址里的凭据与锚点会被去掉，避免写进配置和页面。
func SanitizeEndpoint(raw string) (string, bool) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" || len(endpoint) > maxCustomEndpointLength {
		return "", false
	}
	if strings.ContainsAny(endpoint, " \t\r\n") || strings.ContainsRune(endpoint, 0) {
		return "", false
	}
	lowered := strings.ToLower(endpoint)
	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" {
			return "", false
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", false
		}
		parsed.User = nil
		parsed.Fragment = ""
		parsed.RawFragment = ""
		return parsed.String(), true
	}
	if strings.Contains(endpoint, "://") {
		return "", false
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	return endpoint, true
}

// ValidateCustomEndpoints 是保存设置时的严格校验：任何一条不合法就整体拒绝，
// 避免管理员以为配好了、实际被悄悄丢掉。空值表示"未配置"，直接跳过。
func ValidateCustomEndpoints(endpoints map[string]string) (map[string]string, error) {
	cleaned := make(map[string]string, len(endpoints))
	for channelID, endpoint := range endpoints {
		if strings.TrimSpace(endpoint) == "" {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(channelID))
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid channel id: %s", channelID)
		}
		sanitized, ok := SanitizeEndpoint(endpoint)
		if !ok {
			return nil, fmt.Errorf("invalid price endpoint for channel %d", id)
		}
		cleaned[strconv.Itoa(id)] = sanitized
	}
	if len(cleaned) > maxCustomEndpointEntries {
		return nil, fmt.Errorf("too many price endpoints: %d", len(cleaned))
	}
	return cleaned, nil
}

// IsNormalized 报告配置是否已经是清洗后的形态，用于保存前拒绝越界或非法的取值。
// 不能直接用 `setting == setting.Normalized()`：结构体里有 map 字段，不可比较。
func (setting PriceMonitorSetting) IsNormalized() bool {
	normalized := setting.Normalized()
	return setting.Enabled == normalized.Enabled &&
		setting.IntervalMinutes == normalized.IntervalMinutes &&
		setting.TimeoutSeconds == normalized.TimeoutSeconds &&
		setting.IncludeOfficial == normalized.IncludeOfficial &&
		setting.IncludeModelsDev == normalized.IncludeModelsDev &&
		setting.ModelWhitelist == normalized.ModelWhitelist &&
		maps.Equal(setting.CustomEndpoints, normalized.CustomEndpoints)
}

// CustomEndpointFor 返回渠道的人工指定端点，未配置时返回空串。
func (setting PriceMonitorSetting) CustomEndpointFor(channelID int) string {
	if len(setting.CustomEndpoints) == 0 {
		return ""
	}
	return setting.CustomEndpoints[strconv.Itoa(channelID)]
}

// normalizeCustomEndpoints 是运行期的宽松清洗：丢掉不合法条目而不是让整轮巡检失败。
// 必须返回新 map——入参来自发布出去的共享快照，就地改写会和读取方竞争。
func normalizeCustomEndpoints(endpoints map[string]string) map[string]string {
	if len(endpoints) == 0 {
		return nil
	}
	cleaned := make(map[string]string, len(endpoints))
	for channelID, endpoint := range endpoints {
		id, err := strconv.Atoi(strings.TrimSpace(channelID))
		if err != nil || id <= 0 {
			continue
		}
		sanitized, ok := SanitizeEndpoint(endpoint)
		if !ok {
			continue
		}
		cleaned[strconv.Itoa(id)] = sanitized
		if len(cleaned) >= maxCustomEndpointEntries {
			break
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}
