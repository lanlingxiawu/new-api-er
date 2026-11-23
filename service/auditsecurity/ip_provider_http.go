package auditsecurity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func callIPProvider(ctx context.Context, provider operation_setting.IPIntelligenceProvider, ip string, timeoutSeconds int) (IPIntelligenceSnapshot, string, int64) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	if provider.Type != operation_setting.IPProviderCustomIPAPI {
		return IPIntelligenceSnapshot{}, "invalid_provider", 0
	}
	body, _ := common.Marshal(map[string]string{"q": ip, "key": provider.APIKey})
	return requestIPAPI(ctx, provider, provider.Endpoint, "POST", "application/json", body, ip)
}

func requestIPAPI(ctx context.Context, provider operation_setting.IPIntelligenceProvider, endpoint, method, contentType string, body []byte, ip string) (IPIntelligenceSnapshot, string, int64) {
	response, failure, retryAfter := doIPProviderRequest(ctx, provider, endpoint, method, contentType, body)
	if failure != "" {
		return IPIntelligenceSnapshot{}, failure, retryAfter
	}
	var raw struct {
		SchemaVersion int              `json:"schema_version"`
		Sources       []IPDataSource   `json:"sources"`
		Evidence      []IPDataEvidence `json:"evidence"`
		ASN           *struct {
			Number       uint32 `json:"asn"`
			Organization string `json:"org"`
		} `json:"asn"`
		IP          string   `json:"ip"`
		IsVPN       *bool    `json:"is_vpn"`
		IsProxy     *bool    `json:"is_proxy"`
		IsTor       *bool    `json:"is_tor"`
		IsAbuser    *bool    `json:"is_abuser"`
		IsAnycast   *bool    `json:"is_anycast"`
		IsSatellite *bool    `json:"is_satellite"`
		Country     string   `json:"country"`
		Region      string   `json:"region"`
		City        string   `json:"city"`
		Latitude    *float64 `json:"lat"`
		Longitude   *float64 `json:"lon"`
		Docs        string   `json:"docs"`
		Location    *struct {
			Country     string   `json:"country"`
			CountryCode string   `json:"country_code"`
			State       string   `json:"state"`
			City        string   `json:"city"`
			Latitude    *float64 `json:"latitude"`
			Longitude   *float64 `json:"longitude"`
			Accuracy    string   `json:"accuracy"`
		} `json:"location"`
		ErrorCode string `json:"error_code"`
	}
	if err := common.Unmarshal(response, &raw); err != nil || raw.ErrorCode != "" || !validProviderIP(raw.IP, ip) {
		return IPIntelligenceSnapshot{}, "invalid_response", 0
	}
	snapshot := IPIntelligenceSnapshot{
		FieldProviders: map[string]string{}, Anonymous: raw.Docs != "", ObservedAt: time.Now().Unix(),
		IsVPN: raw.IsVPN, IsProxy: raw.IsProxy, IsTor: raw.IsTor, IsAbuser: raw.IsAbuser,
		IsAnycast: raw.IsAnycast, IsSatellite: raw.IsSatellite,
	}
	if raw.Location != nil {
		snapshot.CountryCode = raw.Location.CountryCode
		snapshot.Country = raw.Location.Country
		snapshot.State = raw.Location.State
		snapshot.City = raw.Location.City
		snapshot.Latitude = raw.Location.Latitude
		snapshot.Longitude = raw.Location.Longitude
		snapshot.Accuracy = raw.Location.Accuracy
	} else {
		snapshot.Country, snapshot.State, snapshot.City = raw.Country, raw.Region, raw.City
		snapshot.Latitude, snapshot.Longitude = raw.Latitude, raw.Longitude
	}
	if snapshot.CountryCode != "" || snapshot.Country != "" || snapshot.State != "" || snapshot.City != "" || snapshot.Latitude != nil && snapshot.Longitude != nil {
		snapshot.FieldProviders["geo"] = "response"
	}
	if provider.Type == operation_setting.IPProviderCustomIPAPI && raw.SchemaVersion == 2 {
		if len(raw.Sources) > 32 || len(raw.Evidence) > 32 {
			return IPIntelligenceSnapshot{}, "invalid_response", 0
		}
		applySelfHostedIPMetadata(&snapshot, raw.Sources, raw.Evidence, ip)
		if raw.ASN != nil && raw.ASN.Number > 0 && len(raw.ASN.Organization) <= 800 {
			for _, source := range snapshot.Sources {
				if source.Capability == "geo" {
					snapshot.ASNNumber, snapshot.ASNOrganization = raw.ASN.Number, raw.ASN.Organization
					break
				}
			}
		}
	}
	return snapshot, "", 0
}

// V2 self-hosted responses bind each assertion to a fresh upstream or local dataset.
// Legacy compatible providers keep their existing contract.
func applySelfHostedIPMetadata(snapshot *IPIntelligenceSnapshot, sources []IPDataSource, evidence []IPDataEvidence, ip string) {
	now := time.Now().Unix()
	accepted := map[string]IPDataSource{}
	capabilities := []string{}
	for _, source := range sources {
		if source.Source == "" || len(source.Source) > 64 || len(source.Name) > 200 ||
			!slices.Contains([]string{"geo", "vpn", "proxy", "tor", "abuse"}, source.Capability) ||
			source.DataUpdatedAt <= 0 || source.DataUpdatedAt > now || source.ExpiresAt <= now ||
			source.ExpiresAt > source.DataUpdatedAt+45*86400 {
			continue
		}
		key := source.Source + ":" + source.Capability
		if _, exists := accepted[key]; exists {
			continue
		}
		source.ProviderID = ""
		accepted[key] = source
		snapshot.Sources = append(snapshot.Sources, source)
		capabilities = append(capabilities, source.Capability)
		if snapshot.ExpiresAt == 0 || source.ExpiresAt < snapshot.ExpiresAt {
			snapshot.ExpiresAt = source.ExpiresAt
		}
	}
	*snapshot = filterIPIntelligenceCapabilities(*snapshot, capabilities)
	address, _ := netip.ParseAddr(ip)
	for _, item := range evidence {
		source, exists := accepted[item.Source+":"+item.Capability]
		if !exists || len(item.Reference) > 100 || len(item.MatchedNetwork) > 64 {
			continue
		}
		prefix, err := netip.ParsePrefix(item.MatchedNetwork)
		if err != nil {
			matched, err := netip.ParseAddr(item.MatchedNetwork)
			if err != nil || matched != address {
				continue
			}
		} else if !prefix.Contains(address) {
			continue
		}
		value := map[string]*bool{"vpn": snapshot.IsVPN, "proxy": snapshot.IsProxy, "tor": snapshot.IsTor, "abuse": snapshot.IsAbuser}[item.Capability]
		if value == nil || !*value {
			continue
		}
		item.ProviderID = ""
		item.DataUpdatedAt, item.ExpiresAt = source.DataUpdatedAt, source.ExpiresAt
		snapshot.Evidence = append(snapshot.Evidence, item)
	}
}

func doIPProviderRequest(ctx context.Context, provider operation_setting.IPIntelligenceProvider, endpoint, method, contentType string, body []byte) ([]byte, string, int64) {
	transport := &http.Transport{DialContext: providerDial(provider), TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "invalid_provider", 0
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, "timeout", 0
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return nil, "timeout", 0
		}
		return nil, "request_failed", 0
	}
	defer response.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(limited) > 256*1024 {
		return nil, "invalid_response", 0
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryAfter := int64(0)
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			retryAfter = time.Now().Unix() + int64(seconds)
		}
		return nil, fmt.Sprintf("http_%d", response.StatusCode), retryAfter
	}
	return limited, "", 0
}

func providerDial(provider operation_setting.IPIntelligenceProvider) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("provider address unavailable")
		}
		policy := common.SSRFProtection{AllowPrivateIp: false, ApplyIPFilterForDomain: true}
		for _, addr := range ips {
			if err := policy.ValidateResolvedIP(host, addr.IP); err != nil {
				if !slices.Contains(provider.AllowedPrivateHosts, strings.ToLower(host)) {
					return nil, errors.New("provider address rejected")
				}
			}
		}
		for _, addr := range ips {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("provider connection failed")
	}
}

func validProviderIP(received, expected string) bool {
	if received == "" {
		return false
	}
	a, errA := netip.ParseAddr(received)
	b, errB := netip.ParseAddr(expected)
	return errA == nil && errB == nil && a == b
}
