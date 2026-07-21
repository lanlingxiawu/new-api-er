package common

import (
	"net/smtp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveSMTPGlobals snapshots and restores the SMTP-related package globals so
// each test can mutate them freely (white-box, save/restore per Rule 15).
func saveSMTPGlobals(t *testing.T) {
	t.Helper()
	from, account, server, token := SMTPFrom, SMTPAccount, SMTPServer, SMTPToken
	force, insecure := SMTPForceAuthLogin, SMTPInsecureSkipVerify
	loginList := EmailLoginAuthServerList
	t.Cleanup(func() {
		SMTPFrom, SMTPAccount, SMTPServer, SMTPToken = from, account, server, token
		SMTPForceAuthLogin, SMTPInsecureSkipVerify = force, insecure
		EmailLoginAuthServerList = loginList
	})
}

func TestGenerateMessageID(t *testing.T) {
	saveSMTPGlobals(t)

	SMTPFrom = "noreply@example.com"
	id, err := generateMessageID()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "<"))
	assert.True(t, strings.HasSuffix(id, "@example.com>"))

	SMTPFrom = "invalid-no-at"
	_, err = generateMessageID()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid SMTP account")
}

func TestShouldUseSMTPLoginAuth(t *testing.T) {
	saveSMTPGlobals(t)

	// Force flag short-circuits everything.
	SMTPForceAuthLogin = true
	assert.True(t, shouldUseSMTPLoginAuth())

	SMTPForceAuthLogin = false
	SMTPAccount = "user@outlook.com"
	SMTPServer = "smtp.office365.com"
	EmailLoginAuthServerList = []string{"smtp.sendcloud.net"}
	assert.True(t, shouldUseSMTPLoginAuth(), "outlook account should use LOGIN")

	// Not outlook, but server is in the login-auth list.
	SMTPAccount = "user@example.com"
	SMTPServer = "smtp.sendcloud.net"
	assert.True(t, shouldUseSMTPLoginAuth())

	// Neither outlook nor listed.
	SMTPAccount = "user@example.com"
	SMTPServer = "smtp.example.com"
	EmailLoginAuthServerList = []string{"smtp.sendcloud.net"}
	assert.False(t, shouldUseSMTPLoginAuth())
}

func TestShouldAuthenticateSMTP(t *testing.T) {
	saveSMTPGlobals(t)

	SMTPAccount, SMTPToken = "u", "p"
	assert.True(t, shouldAuthenticateSMTP())

	SMTPAccount, SMTPToken = "", "p"
	assert.False(t, shouldAuthenticateSMTP())

	SMTPAccount, SMTPToken = "u", ""
	assert.False(t, shouldAuthenticateSMTP())
}

func TestSMTPTLSConfig(t *testing.T) {
	saveSMTPGlobals(t)

	SMTPServer = "smtp.example.com"
	SMTPInsecureSkipVerify = true
	cfg := smtpTLSConfig()
	assert.Equal(t, "smtp.example.com", cfg.ServerName)
	assert.True(t, cfg.InsecureSkipVerify)

	SMTPInsecureSkipVerify = false
	assert.False(t, smtpTLSConfig().InsecureSkipVerify)
}

func TestGetSMTPAuth(t *testing.T) {
	saveSMTPGlobals(t)
	SMTPAccount, SMTPToken = "user", "pass"
	auth := getSMTPAuth()
	_, ok := auth.(*smtpAutoAuth)
	assert.True(t, ok)
}

func TestSMTPAutoAuth_Start(t *testing.T) {
	saveSMTPGlobals(t)
	SMTPServer = "smtp.example.com"

	tlsServer := &smtp.ServerInfo{Name: "smtp.example.com", TLS: true}

	t.Run("force login", func(t *testing.T) {
		SMTPForceAuthLogin = true
		a := &smtpAutoAuth{username: "u", password: "p"}
		mech, resp, err := a.Start(tlsServer)
		require.NoError(t, err)
		assert.Equal(t, "LOGIN", mech)
		assert.Empty(t, resp)
		assert.Equal(t, "LOGIN", a.mech)
	})

	t.Run("ntlm single-mech server keeps ntlm", func(t *testing.T) {
		SMTPForceAuthLogin = false
		SMTPAccount = "u@outlook.com" // makes shouldUseSMTPLoginAuth true
		server := &smtp.ServerInfo{Name: "smtp.example.com", TLS: true, Auth: []string{"NTLM"}}
		a := &smtpAutoAuth{username: "u", password: "p"}
		mech, _, err := a.Start(server)
		require.NoError(t, err)
		assert.Equal(t, "NTLM", mech)
		assert.Equal(t, "NTLM", a.mech)
	})

	t.Run("login when login-auth applies and not single ntlm", func(t *testing.T) {
		SMTPForceAuthLogin = false
		SMTPAccount = "u@outlook.com"
		server := &smtp.ServerInfo{Name: "smtp.example.com", TLS: true, Auth: []string{"LOGIN", "PLAIN"}}
		a := &smtpAutoAuth{username: "u", password: "p"}
		mech, resp, err := a.Start(server)
		require.NoError(t, err)
		assert.Equal(t, "LOGIN", mech)
		assert.Empty(t, resp)
	})

	t.Run("plain preferred", func(t *testing.T) {
		SMTPForceAuthLogin = false
		SMTPAccount = "u@example.com"
		SMTPServer = "smtp.example.com"
		EmailLoginAuthServerList = nil
		server := &smtp.ServerInfo{Name: "smtp.example.com", TLS: true, Auth: []string{"PLAIN"}}
		a := &smtpAutoAuth{username: "u", password: "p"}
		mech, _, err := a.Start(server)
		require.NoError(t, err)
		assert.Equal(t, "PLAIN", mech)
	})

	t.Run("login only server", func(t *testing.T) {
		SMTPForceAuthLogin = false
		SMTPAccount = "u@example.com"
		SMTPServer = "smtp.example.com"
		EmailLoginAuthServerList = nil
		server := &smtp.ServerInfo{Name: "smtp.example.com", TLS: true, Auth: []string{"LOGIN"}}
		a := &smtpAutoAuth{username: "u", password: "p"}
		mech, _, err := a.Start(server)
		require.NoError(t, err)
		assert.Equal(t, "LOGIN", mech)
	})

	t.Run("default plain when no known mech", func(t *testing.T) {
		SMTPForceAuthLogin = false
		SMTPAccount = "u@example.com"
		SMTPServer = "smtp.example.com"
		EmailLoginAuthServerList = nil
		server := &smtp.ServerInfo{Name: "smtp.example.com", TLS: true, Auth: []string{"CRAM-MD5"}}
		a := &smtpAutoAuth{username: "u", password: "p"}
		mech, _, err := a.Start(server)
		require.NoError(t, err)
		assert.Equal(t, "PLAIN", mech)
	})
}

func TestSMTPAutoAuth_Next(t *testing.T) {
	t.Run("no more returns nil", func(t *testing.T) {
		a := &smtpAutoAuth{mech: "LOGIN"}
		resp, err := a.Next(nil, false)
		require.NoError(t, err)
		assert.Nil(t, resp)
	})

	t.Run("login challenges", func(t *testing.T) {
		a := &smtpAutoAuth{username: "user", password: "pass", mech: "LOGIN"}
		u, err := a.Next([]byte("Username:"), true)
		require.NoError(t, err)
		assert.Equal(t, "user", string(u))

		p, err := a.Next([]byte("Password:"), true)
		require.NoError(t, err)
		assert.Equal(t, "pass", string(p))

		_, err = a.Next([]byte("Other:"), true)
		require.Error(t, err)
	})

	t.Run("ntlm challenge errors on garbage", func(t *testing.T) {
		a := &smtpAutoAuth{username: "u", password: "p", mech: "NTLM"}
		_, err := a.Next([]byte("not-a-valid-ntlm-challenge"), true)
		require.Error(t, err)
	})

	t.Run("unexpected mech challenge", func(t *testing.T) {
		a := &smtpAutoAuth{mech: "PLAIN"}
		_, err := a.Next([]byte("x"), true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected SMTP auth challenge")
	})
}

func TestOutlookAuth(t *testing.T) {
	a := &outlookAuth{username: "user", password: "pass"}

	mech, resp, err := a.Start(nil)
	require.NoError(t, err)
	assert.Equal(t, "LOGIN", mech)
	assert.Empty(t, resp)

	u, err := a.Next([]byte("Username:"), true)
	require.NoError(t, err)
	assert.Equal(t, "user", string(u))

	p, err := a.Next([]byte("Password:"), true)
	require.NoError(t, err)
	assert.Equal(t, "pass", string(p))

	_, err = a.Next([]byte("Bogus:"), true)
	require.Error(t, err)

	resp, err = a.Next(nil, false)
	require.NoError(t, err)
	assert.Nil(t, resp)
}

func TestLoginAuthConstructor(t *testing.T) {
	auth := LoginAuth("u", "p")
	_, ok := auth.(*outlookAuth)
	assert.True(t, ok)
}

func TestIsOutlookServer(t *testing.T) {
	assert.True(t, isOutlookServer("user@outlook.com"))
	assert.True(t, isOutlookServer("smtp.office365.onmicrosoft.com"))
	assert.False(t, isOutlookServer("user@gmail.com"))
}

func TestSMTPServerSupportsAuth(t *testing.T) {
	assert.False(t, smtpServerSupportsAuth(nil, "PLAIN"))
	server := &smtp.ServerInfo{Auth: []string{"plain", "LOGIN"}}
	assert.True(t, smtpServerSupportsAuth(server, "PLAIN")) // case-insensitive
	assert.True(t, smtpServerSupportsAuth(server, "login"))
	assert.False(t, smtpServerSupportsAuth(server, "NTLM"))
}
