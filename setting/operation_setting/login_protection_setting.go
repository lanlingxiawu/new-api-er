package operation_setting

import (
	"errors"
	"strconv"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const LoginProtectionModule = "login_protection_setting"

var ErrLoginProtectionSetting = errors.New("invalid login protection setting")

type LoginProtectionSetting struct {
	Enabled     bool `json:"enabled"`
	MaxFailures int  `json:"max_failures"`
}

var loginProtectionSetting = LoginProtectionSetting{MaxFailures: 5}
var loginProtectionSnapshot atomic.Pointer[LoginProtectionSetting]

func init() {
	config.GlobalConfig.Register(LoginProtectionModule, &loginProtectionSetting)
	PublishLoginProtectionSetting()
}

func GetLoginProtectionSetting() LoginProtectionSetting { return *loginProtectionSnapshot.Load() }

func ReplaceLoginProtectionSetting(value LoginProtectionSetting) {
	config.WithConfigDraft(func() { loginProtectionSetting = value })
	PublishLoginProtectionSetting()
}

func PublishLoginProtectionSetting() {
	var value LoginProtectionSetting
	config.WithConfigDraft(func() { value = loginProtectionSetting })
	if value.MaxFailures < 1 || value.MaxFailures > 100 {
		common.SysError("invalid stored login protection threshold; keeping the last valid policy")
		return
	}
	loginProtectionSnapshot.Store(&value)
}

func ParseLoginProtectionUpdate(values map[string]string) (LoginProtectionSetting, error) {
	draft := GetLoginProtectionSetting()
	if len(values) == 0 {
		return draft, ErrLoginProtectionSetting
	}
	for key, value := range values {
		switch key {
		case "enabled":
			if value != "true" && value != "false" {
				return draft, ErrLoginProtectionSetting
			}
			draft.Enabled = value == "true"
		case "max_failures":
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 100 {
				return draft, ErrLoginProtectionSetting
			}
			draft.MaxFailures = limit
		default:
			return draft, ErrLoginProtectionSetting
		}
	}
	return draft, nil
}
