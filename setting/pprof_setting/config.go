package pprof_setting

import (
	"os"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// PprofSetting 只有一个开关：是否允许通过管理端下载 pprof profile。
// 热更新——每个请求现读现判，不缓存、不需要收敛器。
type PprofSetting struct {
	Enabled bool `json:"enabled"`
}

var pprofSetting = PprofSetting{Enabled: false}

func init() {
	config.GlobalConfig.Register("pprof_setting", &pprofSetting)
}

// ApplyEnvDefaults 让旧的 ENABLE_PPROF=true 部署升级后默认开启。
// 仅作为启动默认值：必须在 model.InitOptionMap() 之前调用，
// DB 里存过的值随后会覆盖它（优先级 DB > env > 内置默认）。
func ApplyEnvDefaults() {
	if strings.TrimSpace(os.Getenv("ENABLE_PPROF")) == "true" {
		pprofSetting.Enabled = true
	}
}

// Get 返回配置的值拷贝。
func Get() PprofSetting {
	return pprofSetting
}

// IsEnabled 是否允许 pprof 下载。
func IsEnabled() bool {
	return pprofSetting.Enabled
}
