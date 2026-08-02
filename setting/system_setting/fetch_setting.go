package system_setting

import "github.com/QuantumNous/new-api/setting/config"

type FetchSetting struct {
	EnableSSRFProtection   bool     `json:"enable_ssrf_protection"` // 是否启用SSRF防护
	AllowPrivateIp         bool     `json:"allow_private_ip"`
	DomainFilterMode       bool     `json:"domain_filter_mode"`         // 域名过滤模式，true: 白名单模式，false: 黑名单模式
	IpFilterMode           bool     `json:"ip_filter_mode"`             // IP过滤模式，true: 白名单模式，false: 黑名单模式
	DomainList             []string `json:"domain_list"`                // domain format, e.g. example.com, *.example.com
	IpList                 []string `json:"ip_list"`                    // CIDR format
	AllowedPorts           []string `json:"allowed_ports"`              // port range format, e.g. 80, 443, 8000-9000
	ApplyIPFilterForDomain bool     `json:"apply_ip_filter_for_domain"` // 对域名启用IP过滤（实验性）
}

var defaultFetchSetting = FetchSetting{
	EnableSSRFProtection:   true, // 默认开启SSRF防护
	AllowPrivateIp:         false,
	DomainFilterMode:       false,
	IpFilterMode:           false,
	DomainList:             []string{},
	IpList:                 []string{},
	AllowedPorts:           []string{"80", "443", "8080", "8443"},
	ApplyIPFilterForDomain: true,
}

var fetchSettingSnapshot config.Snapshot[FetchSetting]

func init() {
	// 注册到全局配置管理器，并登记快照发布函数。
	//
	// 这个模块必须走快照：三个 []string 字段的切片头有 3 个字长，反射原地替换时
	// 读侧可能读到"新指针配旧长度"这类非法组合。而它是 SSRF 防护的域名/IP/端口
	// 白名单——读出坏值意味着放行本该拦截的地址。
	config.GlobalConfig.RegisterSnapshot("fetch_setting", &defaultFetchSetting, publishFetchSetting)
}

// publishFetchSetting 只允许在配置草稿锁内调用（由 RegisterSnapshot 保证）。
//
// 快照复制的是结构体，切片字段与草稿共享底层数组；但配置写入是整体替换切片头
// （指向新数组），不会原地改动旧数组，所以旧快照持有的切片始终是一致且不可变的。
func publishFetchSetting() { fetchSettingSnapshot.Publish(defaultFetchSetting) }

// GetFetchSetting 返回不可变快照。通过它写入不会生效。
func GetFetchSetting() *FetchSetting {
	return fetchSettingSnapshot.Load()
}

// ReplaceFetchSetting 整体替换配置并立即重新发布快照。
// 供需要在运行时改这份配置的调用方使用（目前只有测试）。
func ReplaceFetchSetting(s FetchSetting) {
	config.WithConfigDraft(func() {
		defaultFetchSetting = s
		publishFetchSetting()
	})
}
