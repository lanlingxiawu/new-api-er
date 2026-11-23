package auditsecurity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"math"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

type IPIntelligenceSnapshot struct {
	Providers       []string          `json:"providers,omitempty"`
	FieldProviders  map[string]string `json:"field_providers,omitempty"`
	Anonymous       bool              `json:"anonymous,omitempty"`
	ObservedAt      int64             `json:"observed_at"`
	CountryCode     string            `json:"country_code,omitempty"`
	Country         string            `json:"country,omitempty"`
	State           string            `json:"state,omitempty"`
	City            string            `json:"city,omitempty"`
	Latitude        *float64          `json:"latitude,omitempty"`
	Longitude       *float64          `json:"longitude,omitempty"`
	Accuracy        string            `json:"accuracy,omitempty"`
	IsVPN           *bool             `json:"is_vpn"`
	IsProxy         *bool             `json:"is_proxy"`
	IsTor           *bool             `json:"is_tor"`
	IsAbuser        *bool             `json:"is_abuser"`
	IsAnycast       *bool             `json:"is_anycast,omitempty"`
	IsSatellite     *bool             `json:"is_satellite,omitempty"`
	ASNNumber       uint32            `json:"asn_number,omitempty"`
	ASNOrganization string            `json:"asn_organization,omitempty"`
	Sources         []IPDataSource    `json:"sources,omitempty"`
	Evidence        []IPDataEvidence  `json:"evidence,omitempty"`
	ExpiresAt       int64             `json:"expires_at,omitempty"`
}

type IPDataSource struct {
	ProviderID    string `json:"provider_id,omitempty"`
	Source        string `json:"source"`
	Name          string `json:"name"`
	Capability    string `json:"capability"`
	DataUpdatedAt int64  `json:"data_updated_at"`
	ExpiresAt     int64  `json:"expires_at"`
}

type IPDataEvidence struct {
	ProviderID     string `json:"provider_id,omitempty"`
	Source         string `json:"source"`
	Capability     string `json:"capability"`
	MatchedNetwork string `json:"matched_network"`
	Reference      string `json:"reference,omitempty"`
	DataUpdatedAt  int64  `json:"data_updated_at"`
	ExpiresAt      int64  `json:"expires_at"`
}

type ipProviderCacheEntry struct {
	snapshot IPIntelligenceSnapshot
	expires  int64
}

type ipIntelligenceManager struct {
	mu    sync.Mutex
	cache map[string]ipProviderCacheEntry
}

var globalIPIntelligence = &ipIntelligenceManager{cache: map[string]ipProviderCacheEntry{}}

func (m *ipIntelligenceManager) lookup(ctx context.Context, db *gorm.DB, ip string, cfg operation_setting.AuditSecuritySetting, maxCalls int) (IPIntelligenceSnapshot, int) {
	providers, err := operation_setting.ParseIPIntelligenceProviders(cfg.IPIntelligenceProvidersJSON)
	if err != nil {
		return IPIntelligenceSnapshot{FieldProviders: map[string]string{}}, 0
	}
	return m.lookupProviders(ctx, db, ip, cfg, providers, maxCalls)
}

func (m *ipIntelligenceManager) lookupProviders(ctx context.Context, db *gorm.DB, ip string, cfg operation_setting.AuditSecuritySetting, providers []operation_setting.IPIntelligenceProvider, maxCalls int) (IPIntelligenceSnapshot, int) {
	merged := IPIntelligenceSnapshot{FieldProviders: map[string]string{}}
	selected := operation_setting.ActiveIPIntelligenceProvider(providers)
	if selected == nil || !publicIPForIntelligence(ip) {
		return merged, 0
	}
	provider := *selected
	hash := ipProviderConfigHash(provider)
	cacheKey := provider.ID + ":" + hash + ":" + ip
	if snapshot, ok := m.cached(cacheKey); ok {
		mergeIPIntelligence(&merged, snapshot, provider.ID)
		return merged, 0
	}
	if maxCalls < 1 {
		return merged, 0
	}
	reserved, err := reserveIPProviderCall(ctx, db, provider, hash)
	if err != nil {
		common.SysError("audit IP intelligence quota reservation failed")
		return merged, 0
	}
	if !reserved {
		return merged, 0
	}
	snapshot, failure, retryAfter := callIPProvider(ctx, provider, ip, cfg.IPIntelligenceTimeoutSeconds)
	recordIPProviderResult(ctx, db, provider, hash, failure, retryAfter)
	if failure != "" {
		return merged, 1
	}
	normalizeIPIntelligenceCoordinates(&snapshot)
	snapshot = filterIPIntelligenceCapabilities(snapshot, provider.Capabilities)
	m.store(cacheKey, snapshot, time.Now().Unix()+ipProviderCacheTTLSeconds(provider, snapshot, cfg))
	mergeIPIntelligence(&merged, snapshot, provider.ID)
	return merged, 1
}

func (m *ipIntelligenceManager) cached(key string) (IPIntelligenceSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.cache[key]
	if !ok || entry.expires <= time.Now().Unix() {
		delete(m.cache, key)
		return IPIntelligenceSnapshot{}, false
	}
	return entry.snapshot, true
}

func (m *ipIntelligenceManager) store(key string, snapshot IPIntelligenceSnapshot, expires int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cache) >= 10000 {
		for key := range m.cache {
			delete(m.cache, key)
			break
		}
	}
	m.cache[key] = ipProviderCacheEntry{snapshot: snapshot, expires: expires}
}

func updateMissingCapabilities(missing map[string]bool, snapshot IPIntelligenceSnapshot) {
	if reliableGeoForTravel(snapshot) {
		missing["geo"] = false
	}
	for capability, value := range map[string]*bool{"vpn": snapshot.IsVPN, "proxy": snapshot.IsProxy, "tor": snapshot.IsTor, "abuse": snapshot.IsAbuser} {
		if value != nil {
			missing[capability] = false
		}
	}
}

func mergeIPIntelligence(target *IPIntelligenceSnapshot, source IPIntelligenceSnapshot, providerID string) {
	if source.ObservedAt > target.ObservedAt && ipIntelligenceHasData(source) {
		target.ObservedAt = source.ObservedAt
	}
	target.Anonymous = target.Anonymous || source.Anonymous
	if source.ASNNumber != 0 && target.ASNNumber == 0 {
		target.ASNNumber, target.ASNOrganization = source.ASNNumber, source.ASNOrganization
		target.FieldProviders["asn"] = providerID
	}
	sourceQuality, targetQuality := geoCoverageQuality(source), geoCoverageQuality(*target)
	if sourceQuality > targetQuality {
		target.CountryCode, target.Country, target.State, target.City = source.CountryCode, source.Country, source.State, source.City
		target.Latitude, target.Longitude, target.Accuracy = source.Latitude, source.Longitude, source.Accuracy
		target.FieldProviders["geo"] = providerID
	}
	for capability, value := range map[string]*bool{"vpn": source.IsVPN, "proxy": source.IsProxy, "tor": source.IsTor, "abuse": source.IsAbuser} {
		if value == nil || target.FieldProviders[capability] != "" {
			continue
		}
		switch capability {
		case "vpn":
			target.IsVPN = value
		case "proxy":
			target.IsProxy = value
		case "tor":
			target.IsTor = value
		case "abuse":
			target.IsAbuser = value
		}
		target.FieldProviders[capability] = providerID
	}
	if source.IsAnycast != nil && (*source.IsAnycast || target.IsAnycast == nil) {
		target.IsAnycast = source.IsAnycast
	}
	if source.IsSatellite != nil && (*source.IsSatellite || target.IsSatellite == nil) {
		target.IsSatellite = source.IsSatellite
	}
	for _, item := range source.Sources {
		if len(target.Sources) >= 32 {
			break
		}
		item.ProviderID = providerID
		target.Sources = append(target.Sources, item)
	}
	for _, item := range source.Evidence {
		if target.FieldProviders[item.Capability] != providerID {
			continue
		}
		if len(target.Evidence) >= 32 {
			break
		}
		item.ProviderID = providerID
		target.Evidence = append(target.Evidence, item)
	}
	if source.ExpiresAt > 0 && (target.ExpiresAt == 0 || source.ExpiresAt < target.ExpiresAt) {
		target.ExpiresAt = source.ExpiresAt
	}
	target.Providers = append(target.Providers, providerID)
}

func ipIntelligenceHasData(snapshot IPIntelligenceSnapshot) bool {
	return snapshot.ASNNumber != 0 || snapshot.FieldProviders["geo"] != "" || snapshot.IsVPN != nil || snapshot.IsProxy != nil || snapshot.IsTor != nil || snapshot.IsAbuser != nil || snapshot.IsAnycast != nil || snapshot.IsSatellite != nil
}

func filterIPIntelligenceCapabilities(snapshot IPIntelligenceSnapshot, capabilities []string) IPIntelligenceSnapshot {
	snapshot.FieldProviders = maps.Clone(snapshot.FieldProviders)
	if !slices.Contains(capabilities, "geo") {
		snapshot.CountryCode, snapshot.Country, snapshot.State, snapshot.City = "", "", "", ""
		snapshot.Latitude, snapshot.Longitude, snapshot.Accuracy = nil, nil, ""
		snapshot.IsAnycast, snapshot.IsSatellite = nil, nil
		delete(snapshot.FieldProviders, "geo")
		snapshot.ASNNumber, snapshot.ASNOrganization = 0, ""
		delete(snapshot.FieldProviders, "asn")
	}
	for capability, target := range map[string]**bool{"vpn": &snapshot.IsVPN, "proxy": &snapshot.IsProxy, "tor": &snapshot.IsTor, "abuse": &snapshot.IsAbuser} {
		if !slices.Contains(capabilities, capability) {
			*target = nil
			delete(snapshot.FieldProviders, capability)
		}
	}
	snapshot.Sources = slices.DeleteFunc(slices.Clone(snapshot.Sources), func(item IPDataSource) bool {
		return !slices.Contains(capabilities, item.Capability)
	})
	snapshot.Evidence = slices.DeleteFunc(slices.Clone(snapshot.Evidence), func(item IPDataEvidence) bool {
		return !slices.Contains(capabilities, item.Capability)
	})
	return snapshot
}

func geoCoverageQuality(snapshot IPIntelligenceSnapshot) int {
	quality := 0
	if snapshot.CountryCode != "" || snapshot.Country != "" || snapshot.State != "" || snapshot.City != "" {
		quality = 1
	}
	if validIPIntelligenceCoordinates(snapshot.Latitude, snapshot.Longitude) {
		quality = 2
		if reliableGeoAccuracy(snapshot.Accuracy) {
			quality = 3
		}
	}
	return quality
}

func reliableGeoAccuracy(accuracy string) bool {
	switch accuracy {
	case "VERY_HIGH", "HIGH", "MEDIUM", "LOW":
		return true
	default:
		return false
	}
}

func reliableGeoForTravel(snapshot IPIntelligenceSnapshot) bool {
	return validIPIntelligenceCoordinates(snapshot.Latitude, snapshot.Longitude) && reliableGeoAccuracy(snapshot.Accuracy) && (snapshot.IsAnycast == nil || !*snapshot.IsAnycast) && (snapshot.IsSatellite == nil || !*snapshot.IsSatellite)
}

func validIPIntelligenceCoordinates(latitude, longitude *float64) bool {
	return latitude != nil && longitude != nil && !math.IsNaN(*latitude) && !math.IsNaN(*longitude) && !math.IsInf(*latitude, 0) && !math.IsInf(*longitude, 0) && math.Abs(*latitude) <= 90 && math.Abs(*longitude) <= 180
}

func normalizeIPIntelligenceCoordinates(snapshot *IPIntelligenceSnapshot) {
	if !validIPIntelligenceCoordinates(snapshot.Latitude, snapshot.Longitude) {
		snapshot.Latitude = nil
		snapshot.Longitude = nil
		snapshot.Accuracy = ""
	}
}

func ipProviderCacheTTLSeconds(provider operation_setting.IPIntelligenceProvider, snapshot IPIntelligenceSnapshot, cfg operation_setting.AuditSecuritySetting) int64 {
	ttl := int64(cfg.IPIntelligenceCacheTTLMinutes) * 60
	if snapshot.ExpiresAt > 0 {
		ttl = min(ttl, max(int64(0), snapshot.ExpiresAt-time.Now().Unix()))
	}
	// Upstream TTLs, quotas and rotation live at the self-hosted service.
	// Only coalesce adjacent worker lookups locally.
	return min(ttl, 60)
}

func ipProviderConfigHash(provider operation_setting.IPIntelligenceProvider) string {
	raw, _ := common.Marshal(provider)
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, _ = mac.Write([]byte("audit-ip-provider-v1\x00"))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func publicIPForIntelligence(ip string) bool {
	parsed, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	policy := common.SSRFProtection{AllowPrivateIp: false, ApplyIPFilterForDomain: true}
	return policy.ValidateResolvedIP(ip, parsed.AsSlice()) == nil
}
