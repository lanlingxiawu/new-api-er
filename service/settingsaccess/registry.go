package settingsaccess

const (
	ScopeBillingGroupPricing  = "billing.group-pricing"
	ScopeChannelProfitPreview = "channel.profit-preview"
	ScopeVeridropDetection    = "veridrop-detection"
	// ScopeCommissionTierReset 覆盖员工页的「提成周期重置」卡片。它不是系统设置页的分区，
	// 权限跟随员工管理菜单（见 middleware/system_settings_auth.go），与同一张卡片上的
	// 「立即重置」「安全切换」保持同一道门槛。
	ScopeCommissionTierReset = "employees.commission-tier-reset"
)

type Definition struct {
	Scope      string
	OptionKeys map[string]struct{}
	GroupKeys  map[string]map[string]struct{}
}

var definitions = buildDefinitions()

func Resolve(scope string) (Definition, bool) {
	definition, ok := definitions[scope]
	return definition, ok
}

func Scopes() []string {
	result := make([]string, 0, len(definitions))
	for scope := range definitions {
		result = append(result, scope)
	}
	return result
}

func AllowsOption(scope string, key string) bool {
	definition, ok := definitions[scope]
	if !ok {
		return false
	}
	_, ok = definition.OptionKeys[key]
	return ok
}

func OptionKeys(scope string) (map[string]struct{}, bool) {
	definition, ok := definitions[scope]
	if !ok {
		return nil, false
	}
	result := make(map[string]struct{}, len(definition.OptionKeys))
	for key := range definition.OptionKeys {
		result[key] = struct{}{}
	}
	return result, true
}

func AllowsGroup(scope string, module string, values map[string]string) bool {
	if len(values) == 0 {
		return false
	}
	definition, ok := definitions[scope]
	if !ok {
		return false
	}
	allowedKeys, ok := definition.GroupKeys[module]
	if !ok {
		return false
	}
	for key := range values {
		if _, allowed := allowedKeys[key]; !allowed {
			return false
		}
	}
	return true
}

func keys(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func scope(scope string, optionKeys ...string) Definition {
	return Definition{Scope: scope, OptionKeys: keys(optionKeys...), GroupKeys: map[string]map[string]struct{}{}}
}

func configScope(scopeName string, module string, optionKeys []string, groupKeys []string) Definition {
	return Definition{
		Scope:      scopeName,
		OptionKeys: keys(optionKeys...),
		GroupKeys:  map[string]map[string]struct{}{module: keys(groupKeys...)},
	}
}
