package infini_setting

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestV2Signing(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	require.NoError(t, err)
	previous := *GetInfiniSetting()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("infini_setting", map[string]string{"key_id": previous.KeyID, "private_key": previous.PrivateKey}))
	})
	require.NoError(t, config.GlobalConfig.UpdateFromMap("infini_setting", map[string]string{
		"key_id": strings.Repeat("A", 32), "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	}))
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			var body []byte
			if method == "POST" {
				body = []byte(`{"amount":"1.00"}`)
			}
			req := httptest.NewRequest(method, "https://example.com/v2/acquiring/order?order_id=a%2Fb%2B+c", nil)
			require.NoError(t, SignRequest(req, body))
			h := sha256.Sum256(body)
			require.Equal(t, "SHA-256="+base64.StdEncoding.EncodeToString(h[:]), req.Header.Get("Digest"))
			nonce, err := base64.RawURLEncoding.DecodeString(req.Header.Get("X-API-Nonce"))
			require.NoError(t, err)
			require.Len(t, nonce, 16)
			payload := strings.Join([]string{"INFINI-OPENAPI-V2", strings.Repeat("A", 32), method, req.URL.RequestURI(), req.Header.Get("X-API-Timestamp"), req.Header.Get("X-API-Nonce"), req.Header.Get("Digest")}, "\n") + "\n"
			sig, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(req.Header.Get("Authorization"), `Ed25519 signature="`), `"`))
			require.NoError(t, err)
			require.True(t, ed25519.Verify(public, []byte(payload), sig))
			require.False(t, ed25519.Verify(public, []byte(payload+"changed"), sig))
			require.Empty(t, req.Header.Get("Date"))
			oldNonce := req.Header.Get("X-API-Nonce")
			require.NoError(t, SignRequest(req, body))
			require.NotEqual(t, oldNonce, req.Header.Get("X-API-Nonce"))
		})
	}
}

func TestV2CredentialValidation(t *testing.T) {
	for _, key := range []string{"", strings.Repeat("A", 31), strings.Repeat("A", 33), strings.Repeat("0", 32), strings.Repeat("I", 32)} {
		require.Error(t, ValidateKeyID(key))
	}
	require.NoError(t, ValidateKeyID(strings.Repeat("A", 32)))
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	require.NoError(t, err)
	valid := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	require.NoError(t, ValidatePrivateKey(valid))
	for _, key := range []string{"", "legacy-hmac-secret", valid + valid, valid + "junk", "junk\n" + valid, strings.ReplaceAll(valid, "PRIVATE KEY", "OPENSSH PRIVATE KEY"), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")}))} {
		require.Error(t, ValidatePrivateKey(key))
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	otherDER, err := x509.MarshalPKCS8PrivateKey(other)
	require.NoError(t, err)
	require.Error(t, ValidatePrivateKey(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherDER}))))
	require.NoError(t, ValidateOption("infini_setting.key_id", ""))
	require.Error(t, ValidateOption("infini_setting.key_id", "invalid"))
	require.NoError(t, ValidateOption("infini_setting.private_key", valid))
	require.Error(t, ValidateOption("infini_setting.unknown", valid))
}
