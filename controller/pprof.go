package controller

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/pprof_setting"

	"github.com/gin-gonic/gin"
)

// pprof 下载：仅 root 可用（路由组已 RootAuth）。heap / goroutine dump 会带出
// 内存中的上游密钥与用户令牌，权限必须最高档。

const (
	defaultProfileSeconds = 30
	// CPU / trace 单次采样时长硬上限：防止一个请求把 profiler 占住很久。
	maxProfileSeconds = 120
)

// GetPprofStatus 返回开关状态，供前端渲染开关与下载按钮。
func GetPprofStatus(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"enabled":  pprof_setting.IsEnabled(),
		"profiles": service.AllProfiles,
	})
}

// ServePprofProfile 下载原始 profile，交给标准库 net/http/pprof 处理。
func ServePprofProfile(c *gin.Context) {
	if !pprof_setting.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgPprofDisabled)
		return
	}

	name := strings.Trim(c.Param("name"), "/")
	if name != "" && !service.IsKnownProfile(name) {
		common.ApiErrorI18n(c, i18n.MsgPprofInvalidProfile)
		return
	}

	if name == service.ProfileCPU || name == service.ProfileTrace {
		seconds, ok := parseProfileSeconds(c)
		if !ok {
			return
		}
		query := c.Request.URL.Query()
		query.Set("seconds", strconv.Itoa(seconds))
		c.Request.URL.RawQuery = query.Encode()
	}

	err := service.ServePprofByName(c.Writer, c.Request, name)
	switch {
	case err == nil:
		return
	case errors.Is(err, service.ErrCPUProfileBusy):
		common.ApiErrorI18n(c, i18n.MsgPprofBusy)
	case errors.Is(err, service.ErrTraceBusy):
		common.ApiErrorI18n(c, i18n.MsgPprofTraceBusy)
	default:
		logger.LogError(c, "serve pprof profile failed: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgPprofInvalidProfile)
	}
}

// parseProfileSeconds 超上限直接拒绝而不是静默截断，避免用户以为采了更久。
func parseProfileSeconds(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("seconds"))
	if raw == "" {
		return defaultProfileSeconds, true
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		common.ApiErrorI18n(c, i18n.MsgPprofInvalidSeconds)
		return 0, false
	}
	if seconds > maxProfileSeconds {
		common.ApiErrorI18n(c, i18n.MsgPprofSecondsTooLarge, map[string]any{"Max": maxProfileSeconds})
		return 0, false
	}
	return seconds, true
}
