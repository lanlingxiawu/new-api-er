package service

import (
	"errors"
	"net/http"
	nethttppprof "net/http/pprof"
	"sync"
)

// pprof profile 下载：直接复用标准库 net/http/pprof 的能力，按需从管理端拉取
// profile 文件（heap / cpu / goroutine / allocs / block / mutex / threadcreate / trace）。
// 开关 pprof_setting.Enabled 热更新，每个请求现读现判。

// ErrCPUProfileBusy 全局只允许一个 CPU profile 在跑，抢不到时返回它。
var ErrCPUProfileBusy = errors.New("cpu profiler busy")

// ErrTraceBusy runtime trace 同样是全局独占的。
var ErrTraceBusy = errors.New("trace busy")

var (
	cpuProfileMu sync.Mutex
	traceMu      sync.Mutex
)

// 与标准库 net/http/pprof 的路径名一致。
const (
	ProfileCPU   = "profile"
	ProfileTrace = "trace"
)

// AllProfiles 可下载的 profile 列表（下发给前端渲染下载按钮）。
// 与 net/http/pprof 暴露的一致；block/mutex 未设采样率时为空，属 pprof 固有行为。
var AllProfiles = []string{
	ProfileCPU, "heap", "goroutine", "allocs",
	"block", "mutex", "threadcreate", ProfileTrace,
}

// IsKnownProfile 校验 profile 名，防止把任意路径透传给标准库 handler。
func IsKnownProfile(name string) bool {
	switch name {
	case ProfileCPU, ProfileTrace, "heap", "goroutine", "allocs",
		"block", "mutex", "threadcreate", "cmdline", "symbol":
		return true
	}
	return false
}

// ServePprofByName 交给标准库的 pprof handler 处理。name 为空表示索引页。
// CPU / trace 是全局独占资源，抢不到时返回错误且不写响应体，由调用方决定回应形式。
func ServePprofByName(w http.ResponseWriter, r *http.Request, name string) error {
	// 标准库的 Index / Handler 依赖 /debug/pprof/ 前缀解析名字与生成链接，
	// 挂到别的路径上时必须改写 URL，否则索引页链接全是坏的。
	clone := r.Clone(r.Context())
	if name == "" {
		clone.URL.Path = "/debug/pprof/"
	} else {
		clone.URL.Path = "/debug/pprof/" + name
	}

	switch name {
	case "":
		nethttppprof.Index(w, clone)
	case ProfileCPU:
		if !cpuProfileMu.TryLock() {
			return ErrCPUProfileBusy
		}
		defer cpuProfileMu.Unlock()
		nethttppprof.Profile(w, clone)
	case ProfileTrace:
		if !traceMu.TryLock() {
			return ErrTraceBusy
		}
		defer traceMu.Unlock()
		nethttppprof.Trace(w, clone)
	case "cmdline":
		nethttppprof.Cmdline(w, clone)
	case "symbol":
		nethttppprof.Symbol(w, clone)
	default:
		nethttppprof.Handler(name).ServeHTTP(w, clone)
	}
	return nil
}
