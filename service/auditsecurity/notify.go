package auditsecurity

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var errDelivery = errors.New("security notification delivery failed")

// The transport resolves and validates the actual dial address, preventing DNS
// rebinding. Proxy environment variables and redirects are deliberately unused.
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errDelivery
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, errDelivery
	}
	policy := common.SSRFProtection{AllowPrivateIp: false, ApplyIPFilterForDomain: true}
	for _, addr := range ips {
		if policy.ValidateResolvedIP(host, addr.IP) != nil {
			return nil, errDelivery
		}
	}
	for _, addr := range ips {
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errDelivery
}

func (w *worker) deliver(ctx context.Context, cfg operation_setting.AuditSecuritySetting) error {
	if err := recoverStaleDeliveries(ctx, w.db); err != nil {
		return err
	}
	if err := w.queueAccountResponseNotifications(ctx, cfg); err != nil {
		return err
	}
	if err := w.deliverAccountNotifications(ctx); err != nil {
		return err
	}
	if !cfg.NotificationEnabled {
		return nil
	}
	var sent int64
	if err := w.db.WithContext(ctx).Model(&model.SecurityDelivery{}).Where("sent_at >= ? AND (kind <> ? OR kind IS NULL)", time.Now().Unix()-3600, "account").Count(&sent).Error; err != nil {
		return err
	}
	if sent >= int64(cfg.MaxNotificationsPerHour) {
		return deferDueDeliveries(ctx, w.db)
	}
	var deliveries []model.SecurityDelivery
	if err := w.db.WithContext(ctx).Where("status = ? AND next_attempt_at <= ? AND (kind <> ? OR kind IS NULL)", "pending", time.Now().Unix(), "account").Order("next_attempt_at ASC").Limit(3).Find(&deliveries).Error; err != nil {
		return err
	}
	for _, d := range deliveries {
		current := operation_setting.GetAuditSecuritySetting()
		if ctx.Err() != nil || current.Suspended || !current.Enabled || !current.NotificationEnabled {
			return nil
		}
		allowed := d.Kind == "webhook" && current.WebhookURL == d.Destination
		if d.Kind == "email" {
			for _, email := range strings.Split(current.NotificationEmails, ";") {
				if strings.TrimSpace(email) == d.Destination {
					allowed = true
				}
			}
		}
		if !allowed {
			if err := updateDeliveryLifecycle(ctx, w.db, d, map[string]any{"status": "cancelled", "wait_reason": "destination_removed"}); err != nil {
				return err
			}
			continue
		}
		var err error
		var alert model.SecurityAlert
		if err := w.db.WithContext(ctx).Omit("evidence").Where("id = ?", d.AlertID).Take(&alert).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if updateErr := updateDeliveryLifecycle(ctx, w.db, d, map[string]any{"status": "cancelled", "wait_reason": "alert_unavailable"}); updateErr != nil {
					return updateErr
				}
				continue
			}
			return err
		}
		alerts := []model.SecurityAlert{alert}
		if err := loadAlertResponses(ctx, w.db, alerts); err != nil {
			return err
		}
		response := alerts[0].Response
		responses := alerts[0].Responses
		messages, waiting := automaticResponseMessages(alert.Rule, responses)
		if waiting {
			if err := updateDeliveryLifecycle(ctx, w.db, d, map[string]any{"next_attempt_at": time.Now().Unix() + 10, "wait_reason": "account_response"}); err != nil {
				return err
			}
			continue
		}
		responseText := strings.Join(messages, "\n")
		claimed, err := claimDelivery(ctx, w.db, d)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		payload, err := common.Marshal(map[string]any{"type": "audit_security_alert", "alert_id": alert.ID, "severity": alert.Severity, "version": d.Version, "response": response, "responses": responses, "response_message": responseText})
		if err != nil {
			return err
		}
		sendCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		if d.Kind == "email" {
			err = sendMail(sendCtx, d.Destination, i18n.Translate("", "audit_security.subject", map[string]any{"ID": alert.ID}), "<p>"+html.EscapeString(i18n.Translate("", "audit_security.body"))+"</p><p>"+html.EscapeString(responseText)+"</p><pre>"+html.EscapeString(string(payload))+"</pre>")
		} else {
			err = sendWebhook(sendCtx, d.Destination, payload, d.ID)
		}
		cancel()
		d.Attempts++
		updates := map[string]any{"attempts": d.Attempts, "wait_reason": ""}
		if err == nil {
			updates["status"] = "sent"
			updates["sent_at"] = time.Now().Unix()
			updates["error_code"] = ""
			updates["next_attempt_at"] = 0
			sent++
		} else {
			updates["status"] = "pending"
			updates["error_code"] = "delivery_failed"
			updates["next_attempt_at"] = time.Now().Unix() + int64(60*d.Attempts*d.Attempts)
			if d.Attempts >= 5 {
				updates["status"] = "failed"
			}
		}
		if err := updateDeliveryLifecycle(ctx, w.db, d, updates); err != nil {
			return err
		}
		if sent >= int64(cfg.MaxNotificationsPerHour) {
			break
		}
	}
	if sent >= int64(cfg.MaxNotificationsPerHour) {
		return deferDueDeliveries(ctx, w.db)
	}
	return nil
}

func automaticResponseMessages(rule string, responses []model.SecurityAccountResponse) ([]string, bool) {
	var messages []string
	for _, response := range responses {
		if response.Source != "automatic" {
			continue
		}
		switch response.Outcome {
		case "pending", "committed", "sync_pending", "clearing":
			return nil, true
		}
		if response.ReasonCode == "stopped" {
			messages = append(messages, i18n.Translate("", "audit_security.response_stopped"))
			continue
		}
		if response.Action == "disable_account" {
			key := ""
			switch response.Outcome {
			case "succeeded":
				key = "ban_succeeded"
			case "failed":
				key = "ban_failed"
			case "partial", "unknown":
				key = "ban_unconfirmed"
			}
			if key != "" {
				messages = append(messages, i18n.Translate("", "audit_security."+key, map[string]any{"id": response.TargetUserID, "rule": i18n.Translate("", "audit_security.rule_"+rule)}))
			}
			continue
		}
		if response.Action != "clear_api_keys" && response.Action != "clear_access_tokens" {
			continue
		}
		key := "cleanup_incomplete"
		switch response.Outcome {
		case "succeeded":
			if response.AffectedCount != nil {
				key = response.Action + "_count"
				if *response.AffectedCount == 0 {
					key = response.Action + "_empty"
				}
			}
		case "failed":
			key = "cleanup_failed"
		case "skipped":
			key = "cleanup_skipped"
		}
		count := int64(0)
		if response.AffectedCount != nil {
			count = *response.AffectedCount
		}
		result := i18n.Translate("", "audit_security."+key, map[string]any{"count": count})
		messages = append(messages, i18n.Translate("", "audit_security.cleanup_result", map[string]any{"id": response.TargetUserID, "action": i18n.Translate("", "audit_security."+response.Action), "result": result}))
	}
	return messages, false
}

func sendWebhook(ctx context.Context, destination string, payload []byte, id int64) error {
	transport := &http.Transport{DialContext: publicDial, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(payload))
	if err != nil {
		return errDelivery
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("audit-security-%d", id))
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

// Delivery shares protocol handling with ordinary mail, but keeps independent
// connections, the worker's context and the public-only Resend transport.
func sendMail(ctx context.Context, to, subject, body string) error {
	common.OptionMapRWMutex.RLock()
	opts := make(map[string]string)
	for _, key := range []string{"EmailProvider", "ResendApiKey", "ResendFrom", "SMTPServer", "SMTPPort", "SMTPFrom", "SMTPAccount", "SMTPToken", "SMTPSSLEnabled", "SMTPStartTLSEnabled", "SMTPInsecureSkipVerify"} {
		opts[key] = common.OptionMap[key]
	}
	common.OptionMapRWMutex.RUnlock()
	var err error
	switch opts["EmailProvider"] {
	case "resend":
		transport := &http.Transport{DialContext: publicDial, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		err = common.SendResendEmail(ctx, client, opts["ResendApiKey"], opts["ResendFrom"], subject, to, body)
	case "", "smtp":
		port, _ := strconv.Atoi(opts["SMTPPort"])
		if port < 1 || port > 65535 {
			return errDelivery
		}
		config := common.SMTPConfig{
			Server: opts["SMTPServer"], Port: port, From: opts["SMTPFrom"],
			SSL: opts["SMTPSSLEnabled"] == "true", StartTLS: opts["SMTPStartTLSEnabled"] == "true",
			TLSConfig: &tls.Config{ServerName: opts["SMTPServer"], MinVersion: tls.VersionTLS12,
				InsecureSkipVerify: opts["SMTPInsecureSkipVerify"] == "true"}, // #nosec G402 -- existing administrator SMTP policy.
		}
		if config.From == "" {
			config.From = opts["SMTPAccount"]
		}
		if opts["SMTPAccount"] != "" && opts["SMTPToken"] != "" {
			config.Auth = common.AutoSMTPAuth(opts["SMTPAccount"], opts["SMTPToken"])
		}
		err = common.SendSMTPEmail(ctx, config, (&net.Dialer{Timeout: 5 * time.Second}).DialContext, subject, to, body)
	default:
		return errDelivery
	}
	if err != nil {
		return errDelivery
	}
	return nil
}
