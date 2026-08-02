package system_setting

import (
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type PasskeySettings struct {
	Enabled              bool   `json:"enabled"`
	RPDisplayName        string `json:"rp_display_name"`
	RPID                 string `json:"rp_id"`
	Origins              string `json:"origins"`
	AllowInsecureOrigin  bool   `json:"allow_insecure_origin"`
	UserVerification     string `json:"user_verification"`
	AttachmentPreference string `json:"attachment_preference"`
}

var defaultPasskeySettings = PasskeySettings{
	Enabled:              false,
	RPDisplayName:        common.SystemName,
	RPID:                 "",
	Origins:              "",
	AllowInsecureOrigin:  false,
	UserVerification:     "preferred",
	AttachmentPreference: "",
}

var passkeySnapshot config.Snapshot[PasskeySettings]

func init() {
	config.GlobalConfig.RegisterSnapshot("passkey", &defaultPasskeySettings, publishPasskeySettings)
}

// derivedPasskeyRPID 缓存首次推导出的 RPID。
//
// 保留"推导一次后固定"的既有语义：RPID 是 WebAuthn 凭据的绑定域，
// 中途变化会让所有已注册的 passkey 失效，所以进程内必须稳定。
var derivedPasskeyRPID atomic.Pointer[string]

// publishPasskeySettings 只允许在配置草稿锁内调用（由 RegisterSnapshot 保证）。
func publishPasskeySettings() {
	// 管理员显式配置了 RPID 时，之前推导的缓存作废。
	if defaultPasskeySettings.RPID != "" {
		derivedPasskeyRPID.Store(nil)
	}
	passkeySnapshot.Publish(defaultPasskeySettings)
}

// GetPasskeySettings 返回不可变快照，并在其上做推导。
//
// RPID / Origins 为空时从 ServerAddress 推导。原实现把推导结果**写回**包级变量，
// 而这个函数会被并发调用——两个请求同时命中就是对同一块内存的并发写。
// 这里改成：推导结果放进独立的原子变量，返回的快照副本只读不写。
// 对外行为与原来一致（RPID 推导一次后固定，Origins 跟随 ServerAddress）。
func GetPasskeySettings() *PasskeySettings {
	stored := passkeySnapshot.Load()
	needsRPID := stored.RPID == "" && ServerAddress != ""
	needsOrigins := stored.Origins == "" || stored.Origins == "[]"
	if !needsRPID && !needsOrigins {
		return stored
	}

	derived := *stored
	if needsRPID {
		if cached := derivedPasskeyRPID.Load(); cached != nil {
			derived.RPID = *cached
		} else {
			rpid := deriveRPIDFromServerAddress(ServerAddress)
			derivedPasskeyRPID.CompareAndSwap(nil, &rpid)
			derived.RPID = *derivedPasskeyRPID.Load()
		}
	}
	if needsOrigins {
		derived.Origins = ServerAddress
	}
	return &derived
}

// deriveRPIDFromServerAddress 从服务器地址取域名作为 RPID。
// ServerAddress 可能是 "https://newapi.pro" 这种带协议的完整地址。
func deriveRPIDFromServerAddress(serverAddress string) string {
	trimmed := strings.TrimSpace(serverAddress)
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return trimmed
}

// ReplacePasskeySettings 整体替换配置并立即重新发布快照（供测试使用）。
func ReplacePasskeySettings(s PasskeySettings) {
	config.WithConfigDraft(func() {
		defaultPasskeySettings = s
		publishPasskeySettings()
	})
}
