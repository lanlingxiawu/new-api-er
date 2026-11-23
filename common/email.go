package common

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"slices"
	"strconv"
	"strings"
	"time"
)

func generateMessageID(from string) (string, error) {
	split := strings.Split(from, "@")
	if len(split) < 2 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := split[1]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

func getSMTPAuth() smtp.Auth {
	return AutoSMTPAuth(SMTPAccount, SMTPToken)
}

func shouldAuthenticateSMTP() bool {
	return SMTPAccount != "" && SMTPToken != ""
}

func smtpTLSConfig() *tls.Config {
	return &tls.Config{
		ServerName:         SMTPServer,
		InsecureSkipVerify: SMTPInsecureSkipVerify, // #nosec G402 -- admin-controlled SMTP compatibility option.
	}
}

func SendEmail(subject string, receiver string, content string) error {
	OptionMapRWMutex.RLock()
	provider := OptionMap["EmailProvider"]
	resendKey := OptionMap["ResendApiKey"]
	resendFrom := OptionMap["ResendFrom"]
	config := SMTPConfig{
		Server: SMTPServer, Port: SMTPPort, From: SMTPFrom, SenderName: SystemName,
		SSL: SMTPSSLEnabled, StartTLS: SMTPStartTLSEnabled, TLSConfig: smtpTLSConfig(),
	}
	if config.From == "" {
		config.From = SMTPAccount
	}
	if shouldAuthenticateSMTP() {
		config.Auth = getSMTPAuth()
	}
	OptionMapRWMutex.RUnlock()
	// One selector controls delivery. Never fall back to a second provider:
	// a failed response does not prove that the first provider did not send.
	switch provider {
	case "resend":
		return sendResendEmail(resendKey, resendFrom, subject, receiver, content)
	case "", "smtp": // Preserve existing installations without a selector.
	default:
		return NewMessage("Email sending method must be SMTP or Resend")
	}
	return SendSMTPEmail(context.Background(), config, nil, subject, receiver, content)
}

// SMTPConfig is a delivery snapshot. The caller owns configuration and network
// isolation; both normal and background mail use the same protocol handling.
type SMTPConfig struct {
	Server, From, SenderName string
	Port                     int
	SSL, StartTLS            bool
	TLSConfig                *tls.Config
	Auth                     smtp.Auth
}

func SendSMTPEmail(ctx context.Context, config SMTPConfig, dialContext func(context.Context, string, string) (net.Conn, error), subject, receiver, content string) error {
	if config.Server == "" {
		return NewMessage("SMTP server is not configured")
	}
	if strings.ContainsAny(config.From+receiver, "\r\n\x00") {
		return NewMessage("Invalid email address")
	}
	sender, err := mail.ParseAddress(config.From)
	if err != nil {
		return NewMessage("Invalid email address")
	}
	if sender.Name == "" {
		sender.Name = config.SenderName
	}
	to := make([]string, 0)
	for address := range strings.SplitSeq(receiver, ";") {
		recipient, err := mail.ParseAddress(strings.TrimSpace(address))
		if err != nil {
			return NewMessage("Invalid email address")
		}
		to = append(to, recipient.Address)
	}
	id, err := generateMessageID(sender.Address)
	if err != nil {
		return err
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	message := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		strings.Join(to, ", "), sender.String(), encodedSubject, time.Now().Format(time.RFC1123Z), id, content))
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if dialContext == nil {
		dialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	conn, err := dialContext(ctx, "tcp", net.JoinHostPort(config.Server, strconv.Itoa(config.Port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	tlsConfig := config.TLSConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: config.Server, MinVersion: tls.VersionTLS12}
	}
	implicitTLS := config.SSL || (config.Port == 465 && !config.StartTLS)
	if implicitTLS {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, config.Server)
	if err != nil {
		return err
	}
	defer client.Close()
	if config.StartTLS && !implicitTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if config.Auth != nil {
		if err = client.Auth(config.Auth); err != nil {
			return err
		}
	}
	if err = client.Mail(sender.Address); err != nil {
		return err
	}
	for _, receiver := range to {
		if err = client.Rcpt(receiver); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(message)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	err = client.Quit()
	if err != nil {
		SysError(LogText("failed to send email to %s: %v", receiver, err))
	}
	return err
}
