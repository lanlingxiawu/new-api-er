package auditsecurity

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// Match the existing four notification protocols, but keep their connections
// and deadlines independent of relay transports. Never return destination URLs.
func sendAccountNotification(ctx context.Context, user model.User, s dto.UserSetting, data dto.Notify, deliveryID int64) error {
	content := accountNotificationContent(data)
	if s.NotifyType == dto.NotifyTypeEmail {
		address := s.NotificationEmail
		if address == "" {
			address = user.Email
		}
		if address == "" {
			return errDelivery
		}
		return sendMail(ctx, address, data.Title, "<p>"+html.EscapeString(content)+"</p>")
	}
	destination, method := "", http.MethodPost
	headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": fmt.Sprintf("audit-account-%d", deliveryID)}
	var body []byte
	var err error
	switch s.NotifyType {
	case dto.NotifyTypeWebhook:
		destination = s.WebhookUrl
		body, err = common.Marshal(service.WebhookPayload{Type: data.Type, Title: data.Title, Content: content, Values: data.Values, Timestamp: time.Now().Unix()})
		if s.WebhookSecret != "" {
			h := hmac.New(sha256.New, []byte(s.WebhookSecret))
			_, _ = h.Write(body)
			headers["X-Webhook-Signature"] = hex.EncodeToString(h.Sum(nil))
			if system_setting.EnableWorker() {
				headers["Authorization"] = "Bearer " + s.WebhookSecret
			}
		}
	case dto.NotifyTypeBark:
		destination = strings.ReplaceAll(strings.ReplaceAll(s.BarkUrl, "{{title}}", url.QueryEscape(data.Title)), "{{content}}", url.QueryEscape(content))
		method = http.MethodGet
		headers["User-Agent"] = "OneAPI-Bark-Notify/1.0"
	case dto.NotifyTypeGotify:
		if s.GotifyUrl == "" || s.GotifyToken == "" {
			return errDelivery
		}
		destination = strings.TrimSuffix(s.GotifyUrl, "/") + "/message?token=" + url.QueryEscape(s.GotifyToken)
		priority := s.GotifyPriority
		if priority < 0 || priority > 10 {
			priority = 5
		}
		body, err = common.Marshal(map[string]any{"title": data.Title, "message": content, "priority": priority})
	default:
		return errDelivery
	}
	if err != nil {
		return errDelivery
	}
	fetch := system_setting.GetFetchSetting()
	var policy *common.SSRFProtection
	if fetch.EnableSSRFProtection {
		policy, err = common.NewSSRFProtectionFromFetchSetting(fetch.AllowPrivateIp, fetch.DomainFilterMode, fetch.IpFilterMode, fetch.DomainList, fetch.IpList, fetch.AllowedPorts, fetch.ApplyIPFilterForDomain)
		if err != nil {
			return errDelivery
		}
	}
	if err = validateAccountNotificationURL(ctx, destination, policy); err != nil {
		return errDelivery
	}
	transport := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	if system_setting.EnableWorker() {
		if !system_setting.WorkerAllowHttpImageRequestEnabled && !strings.HasPrefix(destination, "https://") {
			return errDelivery
		}
		body, err = common.Marshal(service.WorkerRequest{URL: destination, Method: method, Key: system_setting.WorkerValidKey, Headers: headers, Body: body})
		if err != nil {
			return errDelivery
		}
		destination, method = strings.TrimSuffix(system_setting.WorkerUrl, "/")+"/", http.MethodPost
		headers = map[string]string{"Content-Type": "application/json"}
		transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	} else {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errDelivery
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, errDelivery
			}
			for _, ip := range ips {
				if policy != nil && (policy.ApplyIPFilterForDomain || net.ParseIP(host) != nil) && policy.ValidateResolvedIP(host, ip.IP) != nil {
					return nil, errDelivery
				}
			}
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
			}
			return nil, errDelivery
		}
	}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, method, destination, bytes.NewReader(body))
	if err != nil {
		return errDelivery
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return errDelivery
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errDelivery
	}
	return nil
}

func validateAccountNotificationURL(ctx context.Context, raw string, policy *common.SSRFProtection) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errDelivery
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if policy == nil {
		return nil
	}
	// Structural validation does not perform context-free DNS. Resolve below
	// with the send deadline, and repeat validation on the actual dial address.
	structural := *policy
	structural.ApplyIPFilterForDomain = false
	if err := structural.ValidateURL(raw); err != nil {
		return errDelivery
	}
	if !policy.ApplyIPFilterForDomain || net.ParseIP(u.Hostname()) != nil {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil || len(ips) == 0 {
		return errDelivery
	}
	for _, ip := range ips {
		if policy.ValidateResolvedIP(u.Hostname(), ip.IP) != nil {
			return errDelivery
		}
	}
	return nil
}
