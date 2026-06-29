package model

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const businessStatsFallbackFile = "business_stats_fallback"

// businessStatsCircuitBreakerState 熔断器运行时状态（受 businessStatsCircuitBreakerLock 保护）。
type businessStatsCircuitBreakerState struct {
	consecutiveFailures int   // 当前连续失败次数，成功后归零
	cooldownSeconds     int64 // 上次触发的冷却时长（秒），下次触发时翻倍，不随成功重置
	disabledUntil       int64 // 冷却到期时间戳（Unix 秒），超过后自动关闭
	hardDisabled        bool  // fallback 写入失败时置为 true，永久禁用，不可自动恢复
	lastReason          string
}

// BusinessStatsCircuitBreakerStatus 是对外暴露的只读状态视图，用于监控接口。
type BusinessStatsCircuitBreakerStatus struct {
	Enabled             bool   `json:"enabled"`
	ManualDisabled      bool   `json:"manual_disabled"`
	Open                bool   `json:"open"`           // true 表示当前处于开路（跳过记录）状态
	HardDisabled        bool   `json:"hard_disabled"`  // true 表示本进程内永久禁用，重启后自动重置
	DisabledUntil       int64  `json:"disabled_until"` // 冷却到期时间戳，0 表示未触发
	ConsecutiveFailures int    `json:"consecutive_failures"`
	CooldownSeconds     int64  `json:"cooldown_seconds"`
	LastReason          string `json:"last_reason"`
}

var (
	businessStatsCircuitBreakerLock       sync.Mutex
	businessStatsCircuitBreakerStateValue businessStatsCircuitBreakerState
)

// BusinessStatsSideEffectGuard 封装单次侧路操作的生命周期：
//   - Context() 返回带超时的 ctx，传入 DB 查询防止挂起；
//   - Success() 清零连续失败计数；
//   - Fail() 记录失败并视情况触发熔断；
//   - Done() 释放 ctx（应 defer 调用）。
type BusinessStatsSideEffectGuard struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// normalizeBusinessStatsCircuitBreakerSetting 读取配置并补全缺省值，
// 防止外部配置为零值时出现除零或永不触发等边界问题。
func normalizeBusinessStatsCircuitBreakerSetting() operation_setting.BusinessStatsCircuitBreakerSetting {
	cfg := *operation_setting.GetBusinessStatsCircuitBreakerSetting()
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.InitialCooldownSeconds <= 0 {
		cfg.InitialCooldownSeconds = 60
	}
	if cfg.MaxCooldownSeconds < cfg.InitialCooldownSeconds {
		cfg.MaxCooldownSeconds = cfg.InitialCooldownSeconds
	}
	if cfg.SideEffectDBTimeoutMs <= 0 {
		cfg.SideEffectDBTimeoutMs = 800
	}
	return cfg
}

// BusinessStatsSideEffectContext 创建带超时的 context，用于侧路 DB 查询，
// 防止 DB 慢查询阻塞结算 goroutine。
func BusinessStatsSideEffectContext() (context.Context, context.CancelFunc) {
	cfg := normalizeBusinessStatsCircuitBreakerSetting()
	return context.WithTimeout(context.Background(), time.Duration(cfg.SideEffectDBTimeoutMs)*time.Millisecond)
}

// BeginBusinessStatsSideEffect 是侧路操作的统一入口。
// 若熔断器处于开路状态，将跳过信息写入 fallback 日志并返回 false；
// 否则返回带超时 context 的 guard，调用方须 defer guard.Done()。
func BeginBusinessStatsSideEffect(skipKind string, skipPayload any) (*BusinessStatsSideEffectGuard, bool) {
	if IsBusinessStatsCircuitOpen() {
		RecordBusinessStatsSkipped(skipKind, BusinessStatsCircuitSkipReason(), skipPayload)
		return nil, false
	}
	ctx, cancel := BusinessStatsSideEffectContext()
	return &BusinessStatsSideEffectGuard{ctx: ctx, cancel: cancel}, true
}

// Context 返回带超时的 context，传入 DB 调用以防止挂起。
func (g *BusinessStatsSideEffectGuard) Context() context.Context {
	if g == nil || g.ctx == nil {
		return context.Background()
	}
	return g.ctx
}

// Done 释放 context 资源，应在函数入口处 defer 调用。
func (g *BusinessStatsSideEffectGuard) Done() {
	if g != nil && g.cancel != nil {
		g.cancel()
	}
}

// Fail 记录一次侧路失败。err 为 nil 时为空操作（避免调用方判空）。
// 内部调用 ReportBusinessStatsFailure，视累计失败次数决定是否触发熔断。
func (g *BusinessStatsSideEffectGuard) Fail(kind string, err error, payload any) bool {
	if err == nil {
		return false
	}
	ReportBusinessStatsFailure(kind, err.Error(), payload)
	return true
}

// FailReason 与 Fail 相同，但接受字符串原因而非 error，用于非 error 类型的失败场景。
func (g *BusinessStatsSideEffectGuard) FailReason(kind, reason string, payload any) {
	ReportBusinessStatsFailure(kind, reason, payload)
}

// Success 将连续失败计数归零。注意：不重置 cooldownSeconds，
// 因此若系统反复故障，下次触发的冷却时长会在上次基础上翻倍（惩罚递增）。
func (g *BusinessStatsSideEffectGuard) Success() {
	ReportBusinessStatsSuccess()
}

// GetBusinessStatsCircuitBreakerStatus 返回当前熔断器状态的只读视图，供监控接口使用。
func GetBusinessStatsCircuitBreakerStatus() BusinessStatsCircuitBreakerStatus {
	cfg := normalizeBusinessStatsCircuitBreakerSetting()
	now := time.Now().Unix()

	businessStatsCircuitBreakerLock.Lock()
	defer businessStatsCircuitBreakerLock.Unlock()

	return BusinessStatsCircuitBreakerStatus{
		Enabled:             cfg.Enabled,
		ManualDisabled:      cfg.ManualDisabled,
		Open:                cfg.ManualDisabled || (cfg.Enabled && (businessStatsCircuitBreakerStateValue.hardDisabled || businessStatsCircuitBreakerStateValue.disabledUntil > now)),
		HardDisabled:        businessStatsCircuitBreakerStateValue.hardDisabled,
		DisabledUntil:       businessStatsCircuitBreakerStateValue.disabledUntil,
		ConsecutiveFailures: businessStatsCircuitBreakerStateValue.consecutiveFailures,
		CooldownSeconds:     businessStatsCircuitBreakerStateValue.cooldownSeconds,
		LastReason:          businessStatsCircuitBreakerStateValue.lastReason,
	}
}

// IsBusinessStatsCircuitOpen 返回 true 表示熔断器当前处于开路状态，侧路记录应被跳过。
// 开路条件：ManualDisabled=true（手动强制跳过）、hard-disabled、或冷却期内自动开路。
// Enabled=false 时电路视为闭路——副逻辑照常执行，仅关闭自动熔断保护。
func IsBusinessStatsCircuitOpen() bool {
	cfg := normalizeBusinessStatsCircuitBreakerSetting()
	if cfg.ManualDisabled {
		return true
	}
	if !cfg.Enabled {
		return false
	}
	now := time.Now().Unix()
	businessStatsCircuitBreakerLock.Lock()
	defer businessStatsCircuitBreakerLock.Unlock()
	if businessStatsCircuitBreakerStateValue.hardDisabled {
		return true
	}
	return businessStatsCircuitBreakerStateValue.disabledUntil > now
}

// BusinessStatsCircuitSkipReason 返回当前开路的原因描述，用于 fallback 日志。
// 电路闭合时返回空字符串。
func BusinessStatsCircuitSkipReason() string {
	cfg := normalizeBusinessStatsCircuitBreakerSetting()
	if cfg.ManualDisabled {
		return "business stats manually disabled"
	}
	if !cfg.Enabled {
		// Enabled=false 时电路闭合，不会进入此分支
		return ""
	}
	now := time.Now().Unix()
	businessStatsCircuitBreakerLock.Lock()
	defer businessStatsCircuitBreakerLock.Unlock()
	if businessStatsCircuitBreakerStateValue.hardDisabled {
		return "business stats hard disabled after fallback log failure"
	}
	if businessStatsCircuitBreakerStateValue.disabledUntil > now {
		return fmt.Sprintf("business stats circuit open until %d: %s", businessStatsCircuitBreakerStateValue.disabledUntil, businessStatsCircuitBreakerStateValue.lastReason)
	}
	return ""
}

// RecordBusinessStatsSkipped 将一次被跳过的侧路操作写入 fallback 日志。
// 若 fallback 写入本身也失败（队列满），则触发 hard-disable：
// 连跳过记录都无法保存，视为系统进入极端异常状态，停止一切侧路尝试。
func RecordBusinessStatsSkipped(kind, reason string, payload any) {
	if reason == "" {
		reason = "business stats disabled"
	}
	if !writeBusinessStatsFallback(kind, reason, payload) {
		businessStatsCircuitBreakerLock.Lock()
		businessStatsCircuitBreakerStateValue.hardDisabled = true
		businessStatsCircuitBreakerStateValue.lastReason = kind + ": " + reason
		businessStatsCircuitBreakerLock.Unlock()
		common.SysError("business_stats_circuit_breaker: hard disabled because fallback log write failed while skipping")
	}
}

// ReportBusinessStatsSuccess 通知熔断器本次侧路操作成功，将连续失败计数归零。
// 注意：不重置 cooldownSeconds，见 Success() 方法说明。
func ReportBusinessStatsSuccess() {
	businessStatsCircuitBreakerLock.Lock()
	businessStatsCircuitBreakerStateValue.consecutiveFailures = 0
	businessStatsCircuitBreakerLock.Unlock()
}

// ReportBusinessStatsFailure 通知熔断器本次侧路操作失败。
//
// 处理流程：
//  1. Enabled=false 时直接返回（不统计失败）；
//  2. hard-disabled 时直接返回（已处于最终状态）；
//  3. 将失败信息写入 fallback 日志；若写入本身失败（队列满）则触发 hard-disable；
//  4. 累加 consecutiveFailures；
//  5. 未达到 FailureThreshold 时返回，不开断路器；
//  6. 达到阈值时按指数退避计算冷却时长（初次取 InitialCooldownSeconds，
//     后续翻倍，上限 MaxCooldownSeconds），设置 disabledUntil。
func ReportBusinessStatsFailure(kind, reason string, payload any) {
	cfg := normalizeBusinessStatsCircuitBreakerSetting()
	if !cfg.Enabled {
		return
	}
	message := fmt.Sprintf("%s: %s", kind, reason)
	businessStatsCircuitBreakerLock.Lock()
	if businessStatsCircuitBreakerStateValue.hardDisabled {
		businessStatsCircuitBreakerLock.Unlock()
		return
	}
	businessStatsCircuitBreakerLock.Unlock()

	wroteFallback := writeBusinessStatsFallback(kind, reason, payload)

	businessStatsCircuitBreakerLock.Lock()
	defer businessStatsCircuitBreakerLock.Unlock()
	if !wroteFallback {
		businessStatsCircuitBreakerStateValue.hardDisabled = true
		businessStatsCircuitBreakerStateValue.lastReason = message
		common.SysError("business_stats_circuit_breaker: hard disabled because fallback log write failed: " + message)
		return
	}

	businessStatsCircuitBreakerStateValue.consecutiveFailures++
	businessStatsCircuitBreakerStateValue.lastReason = message
	if businessStatsCircuitBreakerStateValue.consecutiveFailures < cfg.FailureThreshold {
		return
	}
	// 达到阈值，开断路器并计算冷却时长（指数退避，multiplier=2）
	if businessStatsCircuitBreakerStateValue.cooldownSeconds <= 0 {
		businessStatsCircuitBreakerStateValue.cooldownSeconds = cfg.InitialCooldownSeconds
	} else {
		businessStatsCircuitBreakerStateValue.cooldownSeconds *= 2
		if businessStatsCircuitBreakerStateValue.cooldownSeconds > cfg.MaxCooldownSeconds {
			businessStatsCircuitBreakerStateValue.cooldownSeconds = cfg.MaxCooldownSeconds
		}
	}
	businessStatsCircuitBreakerStateValue.disabledUntil = time.Now().Unix() + businessStatsCircuitBreakerStateValue.cooldownSeconds
	common.SysError(fmt.Sprintf("business_stats_circuit_breaker: opened for %ds after %d failures: %s",
		businessStatsCircuitBreakerStateValue.cooldownSeconds,
		businessStatsCircuitBreakerStateValue.consecutiveFailures,
		message,
	))
}
