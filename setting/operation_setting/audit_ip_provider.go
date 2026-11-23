package operation_setting

import (
	"errors"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	IPProviderIPAPI       = "ipapi_is"
	IPProviderCustomIPAPI = "custom_ipapi"
	IPProviderProxycheck  = "proxycheck"
	IPProviderIPInfoLite  = "ipinfo_lite"
)

var ipProviderCapabilities = []string{"geo", "vpn", "proxy", "tor", "abuse"}

type IPIntelligenceProvider struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	Endpoint            string   `json:"endpoint"`
	Enabled             bool     `json:"enabled"`
	Priority            int      `json:"priority"`
	APIKey              string   `json:"api_key"`
	KeyConfigured       bool     `json:"key_configured,omitempty"`
	ClearSecret         bool     `json:"clear_secret,omitempty"`
	DailyLimit          int64    `json:"daily_limit"`
	Capabilities        []string `json:"capabilities"`
	AllowedPrivateHosts []string `json:"allowed_private_hosts,omitempty"`
}

func DefaultIPIntelligenceProviders() []IPIntelligenceProvider {
	return []IPIntelligenceProvider{}
}

func defaultIPIntelligenceProvidersJSON() string {
	raw, _ := common.Marshal(DefaultIPIntelligenceProviders())
	return string(raw)
}

func ParseIPIntelligenceProviders(raw string) ([]IPIntelligenceProvider, error) {
	if strings.TrimSpace(raw) == "" {
		return []IPIntelligenceProvider{}, nil
	}
	var providers []IPIntelligenceProvider
	if err := common.UnmarshalJsonStr(raw, &providers); err != nil {
		return nil, errors.New("invalid IP intelligence provider list")
	}
	if len(providers) > 20 {
		return nil, errors.New("IP intelligence provider count outside allowed range")
	}
	seen := make(map[string]bool, len(providers))
	for i := range providers {
		p := &providers[i]
		p.APIKey = strings.TrimSpace(p.APIKey)
		p.KeyConfigured = false
		if !validIPProviderID(p.ID) || seen[p.ID] {
			return nil, errors.New("invalid or duplicate IP intelligence provider ID")
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 100 || len(p.APIKey) > 4096 || strings.ContainsAny(p.APIKey, "\r\n") {
			return nil, errors.New("invalid IP intelligence provider name or credential")
		}
		if p.Priority < 1 || p.Priority > 100000 || p.DailyLimit < 0 || p.DailyLimit > 1000000000 {
			return nil, errors.New("invalid IP intelligence provider priority or daily limit")
		}
		if err := validateIPProviderEndpoint(p.Endpoint); err != nil {
			return nil, err
		}
		switch p.Type {
		case IPProviderIPAPI, IPProviderProxycheck:
			if len(p.Capabilities) == 0 {
				p.Capabilities = slices.Clone(ipProviderCapabilities)
			}
		case IPProviderIPInfoLite:
			p.Capabilities = []string{"geo"}
		case IPProviderCustomIPAPI:
			if len(p.Capabilities) == 0 {
				return nil, errors.New("custom IP intelligence capabilities required")
			}
		default:
			return nil, errors.New("unsupported IP intelligence provider type")
		}
		if err := validateIPProviderCapabilities(p.Capabilities); err != nil {
			return nil, err
		}
		for _, host := range p.AllowedPrivateHosts {
			if !validPrivateProviderHost(host) {
				return nil, errors.New("invalid private IP intelligence host")
			}
		}
	}
	return providers, nil
}

// ActiveIPIntelligenceProvider selects the only callable gateway endpoint.
// Legacy cloud configurations remain readable for migration, never callable.
func ActiveIPIntelligenceProvider(providers []IPIntelligenceProvider) *IPIntelligenceProvider {
	var selected *IPIntelligenceProvider
	for i := range providers {
		provider := &providers[i]
		if provider.Type != IPProviderCustomIPAPI || !provider.Enabled {
			continue
		}
		if selected != nil {
			return nil
		}
		selected = provider
	}
	return selected
}

func MergeIPIntelligenceProviders(raw, previousRaw string) (string, error) {
	incoming, err := ParseIPIntelligenceProviders(raw)
	if err != nil {
		return "", err
	}
	if len(incoming) > 1 || len(incoming) == 1 && incoming[0].Type != IPProviderCustomIPAPI {
		return "", errors.New("configure one self-hosted IP intelligence endpoint")
	}
	var previous []IPIntelligenceProvider
	if strings.TrimSpace(previousRaw) != "" {
		previous, _ = ParseIPIntelligenceProviders(previousRaw)
	}
	keys := make(map[string]string, len(previous))
	for _, p := range previous {
		keys[p.ID] = p.APIKey
	}
	for i := range incoming {
		if incoming[i].ClearSecret {
			incoming[i].APIKey = ""
		} else if incoming[i].APIKey == "" {
			incoming[i].APIKey = keys[incoming[i].ID]
		}
		incoming[i].ClearSecret = false
		incoming[i].KeyConfigured = false
	}
	normalized, err := common.Marshal(incoming)
	if err != nil {
		return "", err
	}
	if _, err = ParseIPIntelligenceProviders(string(normalized)); err != nil {
		return "", err
	}
	return string(normalized), nil
}

func SanitizedIPIntelligenceProvidersJSON(raw string) string {
	providers, err := ParseIPIntelligenceProviders(raw)
	if err != nil {
		return "[]"
	}
	for i := range providers {
		providers[i].KeyConfigured = providers[i].APIKey != ""
		providers[i].APIKey = ""
		providers[i].ClearSecret = false
	}
	encoded, _ := common.Marshal(providers)
	return string(encoded)
}

func validateIPProviderEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid IP intelligence endpoint")
	}
	return nil
}

func validateIPProviderCapabilities(capabilities []string) error {
	seen := map[string]bool{}
	for _, capability := range capabilities {
		if !slices.Contains(ipProviderCapabilities, capability) || seen[capability] {
			return errors.New("invalid IP intelligence capability")
		}
		seen[capability] = true
	}
	return nil
}

func validIPProviderID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i, r := range id {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && (r == '-' || r == '_') {
			continue
		}
		return false
	}
	return true
}

func validPrivateProviderHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\@*?#\r\n\t ") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsValid()
	}
	for _, r := range host {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".") && !strings.Contains(host, "..")
}
