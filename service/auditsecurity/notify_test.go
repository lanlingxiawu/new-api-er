package auditsecurity

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// This server records the actual protocol exchange, including accidental TLS
// negotiation inside an already encrypted connection. It never delivers mail.
func smtpFixture(t *testing.T, implicitTLS bool) (string, <-chan []string) {
	t.Helper()
	certServer := httptest.NewTLSServer(nil)
	tlsConfig := certServer.TLS.Clone()
	certServer.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	commands := make(chan []string, 1)
	go func() {
		var transcript []string
		defer func() { commands <- transcript }()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if implicitTLS {
			conn = tls.Server(conn, tlsConfig)
		}
		_, _ = io.WriteString(conn, "220 localhost ESMTP\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			transcript = append(transcript, line)
			switch {
			case strings.HasPrefix(line, "EHLO"):
				_, _ = io.WriteString(conn, "250-localhost\r\n250 STARTTLS\r\n")
			case line == "STARTTLS":
				if implicitTLS {
					_, _ = io.WriteString(conn, "503 already encrypted\r\n")
					return
				}
				_, _ = io.WriteString(conn, "220 ready\r\n")
				conn = tls.Server(conn, tlsConfig)
				reader = bufio.NewReader(conn)
				implicitTLS = true
			case line == "DATA":
				_, _ = io.WriteString(conn, "354 send message\r\n")
				for {
					part, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if part == ".\r\n" {
						break
					}
					transcript = append(transcript, strings.TrimSpace(part))
				}
				_, _ = io.WriteString(conn, "250 queued\r\n")
			case line == "QUIT":
				_, _ = io.WriteString(conn, "221 bye\r\n")
				return
			default:
				_, _ = io.WriteString(conn, "250 OK\r\n")
			}
		}
	}()
	return listener.Addr().String(), commands
}

func TestNotificationSMTPTransportModes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ssl, startTLS bool
	}{
		{"plain", false, false},
		{"starttls", false, true},
		{"implicit", true, false},
		{"both_flags", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, commands := smtpFixture(t, tc.ssl)
			host, port, err := net.SplitHostPort(address)
			require.NoError(t, err)
			common.OptionMapRWMutex.Lock()
			previous := common.OptionMap
			common.OptionMap = map[string]string{
				"EmailProvider": "smtp", "SMTPServer": host, "SMTPPort": port,
				"SMTPFrom": "sender@example.com", "SMTPSSLEnabled": strconv.FormatBool(tc.ssl),
				"SMTPStartTLSEnabled": strconv.FormatBool(tc.startTLS), "SMTPInsecureSkipVerify": "true",
			}
			common.OptionMapRWMutex.Unlock()
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				common.OptionMap = previous
				common.OptionMapRWMutex.Unlock()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = sendMail(ctx, "recipient@example.com", "notification", "<p>test</p>")
			transcript := <-commands
			require.NoError(t, err, fmt.Sprint(transcript))
			if tc.startTLS && !tc.ssl {
				require.Contains(t, transcript, "STARTTLS")
			} else {
				require.NotContains(t, transcript, "STARTTLS")
			}
			require.Contains(t, transcript, "MAIL FROM:<sender@example.com>")
			require.Contains(t, transcript, "RCPT TO:<recipient@example.com>")
			require.Contains(t, transcript, "<p>test</p>")
			require.Contains(t, transcript, "QUIT")
		})
	}
}

func TestSharedSMTPPortAndTLSPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		port                    int
		ssl, startTLS, implicit bool
	}{
		{"plain", 25, false, false, false},
		{"starttls", 587, false, true, false},
		{"implicit", 25, true, false, true},
		{"both_flags", 25, true, true, true},
		{"implicit_465", 465, false, false, true},
		{"explicit_starttls_465", 465, false, true, false},
		{"both_flags_465", 465, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, commands := smtpFixture(t, tc.implicit)
			config := common.SMTPConfig{
				Server: "smtp.example.com", Port: tc.port, From: "Sender <sender@example.com>",
				SSL: tc.ssl, StartTLS: tc.startTLS,
				TLSConfig: &tls.Config{InsecureSkipVerify: true}, // Local fixture certificate.
			}
			dial := func(ctx context.Context, network, target string) (net.Conn, error) {
				require.Equal(t, net.JoinHostPort(config.Server, strconv.Itoa(tc.port)), target)
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
			err := common.SendSMTPEmail(context.Background(), config, dial, "notification", "one@example.com; Two <two@example.com>", "<p>test</p>")
			transcript := <-commands
			require.NoError(t, err, fmt.Sprint(transcript))
			require.Equal(t, tc.startTLS && !tc.implicit, slices.Contains(transcript, "STARTTLS"))
			require.Contains(t, transcript, "RCPT TO:<one@example.com>")
			require.Contains(t, transcript, "RCPT TO:<two@example.com>")
			require.Contains(t, transcript, "To: one@example.com, two@example.com")
			require.Contains(t, transcript, "MIME-Version: 1.0")
			require.Contains(t, transcript, "<p>test</p>")
		})
	}
}

func TestSharedSMTPContextCancellation(t *testing.T) {
	for _, ssl := range []bool{false, true} {
		t.Run(fmt.Sprint(ssl), func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			defer client.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dialled := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- common.SendSMTPEmail(ctx, common.SMTPConfig{
					Server: "smtp.example.com", Port: 25, From: "sender@example.com", SSL: ssl,
				}, func(context.Context, string, string) (net.Conn, error) {
					close(dialled)
					return client, nil
				}, "notification", "to@example.com", "body")
			}()
			<-dialled
			cancel()
			select {
			case err := <-done:
				require.Error(t, err, "cancellation must interrupt SMTP greeting or TLS handshake")
			case <-time.After(5 * time.Second):
				require.FailNow(t, "SMTP did not stop after cancellation")
			}
		})
	}
}

func TestOrdinaryMailUsesSharedSMTPTransport(t *testing.T) {
	address, commands := smtpFixture(t, true)
	host, portText, err := net.SplitHostPort(address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	oldOptions := common.OptionMap
	oldServer, oldPort, oldFrom := common.SMTPServer, common.SMTPPort, common.SMTPFrom
	oldAccount, oldToken := common.SMTPAccount, common.SMTPToken
	oldSSL, oldStartTLS, oldInsecure := common.SMTPSSLEnabled, common.SMTPStartTLSEnabled, common.SMTPInsecureSkipVerify
	t.Cleanup(func() {
		common.OptionMap = oldOptions
		common.SMTPServer, common.SMTPPort, common.SMTPFrom = oldServer, oldPort, oldFrom
		common.SMTPAccount, common.SMTPToken = oldAccount, oldToken
		common.SMTPSSLEnabled, common.SMTPStartTLSEnabled, common.SMTPInsecureSkipVerify = oldSSL, oldStartTLS, oldInsecure
	})
	common.OptionMap = map[string]string{"EmailProvider": "smtp"}
	common.SMTPServer, common.SMTPPort, common.SMTPFrom = host, port, ""
	common.SMTPAccount, common.SMTPToken = "sender@example.com", ""
	common.SMTPSSLEnabled, common.SMTPStartTLSEnabled, common.SMTPInsecureSkipVerify = true, true, true
	err = common.SendEmail("notification", "to@example.com", "<p>test</p>")
	transcript := <-commands
	require.NoError(t, err, fmt.Sprint(transcript))
	require.NotContains(t, transcript, "STARTTLS")
	require.Contains(t, transcript, "MAIL FROM:<sender@example.com>")
	require.Empty(t, common.SMTPFrom, "sender fallback must not mutate global settings")
}
