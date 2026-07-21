package passkey

import (
	"encoding/base64"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeCredential builds an in-memory PasskeyCredential with a valid base64 credential id.
func makeCredential(t *testing.T) *model.PasskeyCredential {
	t.Helper()
	c := &model.PasskeyCredential{
		UserID:          7,
		CredentialID:    base64.StdEncoding.EncodeToString([]byte("cred-id-bytes")),
		PublicKey:       base64.StdEncoding.EncodeToString([]byte("public-key-bytes")),
		AAGUID:          base64.StdEncoding.EncodeToString([]byte("aaguid")),
		AttestationType: "none",
		SignCount:       42,
		UserPresent:     true,
		UserVerified:    true,
		Attachment:      "platform",
	}
	c.SetTransports([]protocol.AuthenticatorTransport{protocol.USB, protocol.Internal})
	return c
}

// --- NewWebAuthnUser -------------------------------------------------------

func TestNewWebAuthnUser_StoresBothPointers(t *testing.T) {
	u := &model.User{Id: 3, Username: "alice"}
	cred := makeCredential(t)

	wu := NewWebAuthnUser(u, cred)

	require.NotNil(t, wu)
	assert.Same(t, u, wu.ModelUser())
	assert.Same(t, cred, wu.PasskeyCredential())
}

func TestNewWebAuthnUser_AcceptsNilComponents(t *testing.T) {
	wu := NewWebAuthnUser(nil, nil)
	require.NotNil(t, wu)
	assert.Nil(t, wu.ModelUser())
	assert.Nil(t, wu.PasskeyCredential())
}

// --- WebAuthnID ------------------------------------------------------------

func TestWebAuthnID(t *testing.T) {
	cases := []struct {
		name string
		user *model.User
		want []byte
	}{
		{"normal id encodes decimal string", &model.User{Id: 123}, []byte("123")},
		{"zero id still encodes", &model.User{Id: 0}, []byte("0")},
		{"negative id encodes with sign", &model.User{Id: -5}, []byte("-5")},
		{"nil user returns nil", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wu := NewWebAuthnUser(tc.user, nil)
			assert.Equal(t, tc.want, wu.WebAuthnID())
		})
	}
}

func TestWebAuthnID_NilReceiver(t *testing.T) {
	var wu *WebAuthnUser
	assert.Nil(t, wu.WebAuthnID())
}

// --- WebAuthnName ----------------------------------------------------------

func TestWebAuthnName(t *testing.T) {
	cases := []struct {
		name string
		user *model.User
		want string
	}{
		{"trimmed username used", &model.User{Id: 1, Username: "  bob  "}, "bob"},
		{"empty username falls back to user-id", &model.User{Id: 9, Username: ""}, "user-9"},
		{"whitespace-only username falls back", &model.User{Id: 4, Username: "   "}, "user-4"},
		{"nil user returns empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wu := NewWebAuthnUser(tc.user, nil)
			assert.Equal(t, tc.want, wu.WebAuthnName())
		})
	}
}

func TestWebAuthnName_NilReceiver(t *testing.T) {
	var wu *WebAuthnUser
	assert.Equal(t, "", wu.WebAuthnName())
}

// --- WebAuthnDisplayName ---------------------------------------------------

func TestWebAuthnDisplayName(t *testing.T) {
	cases := []struct {
		name string
		user *model.User
		want string
	}{
		{"display name used when set", &model.User{Id: 1, Username: "bob", DisplayName: " Bob Smith "}, "Bob Smith"},
		{"blank display falls back to name", &model.User{Id: 2, Username: "carol", DisplayName: "  "}, "carol"},
		{"empty display + empty username falls back to user-id", &model.User{Id: 8, Username: "", DisplayName: ""}, "user-8"},
		{"nil user returns empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wu := NewWebAuthnUser(tc.user, nil)
			assert.Equal(t, tc.want, wu.WebAuthnDisplayName())
		})
	}
}

func TestWebAuthnDisplayName_NilReceiver(t *testing.T) {
	var wu *WebAuthnUser
	assert.Equal(t, "", wu.WebAuthnDisplayName())
}

// --- WebAuthnCredentials ---------------------------------------------------

func TestWebAuthnCredentials_ReturnsMappedCredential(t *testing.T) {
	cred := makeCredential(t)
	wu := NewWebAuthnUser(&model.User{Id: 7, Username: "dave"}, cred)

	creds := wu.WebAuthnCredentials()
	require.Len(t, creds, 1)

	got := creds[0]
	assert.Equal(t, []byte("cred-id-bytes"), got.ID)
	assert.Equal(t, []byte("public-key-bytes"), got.PublicKey)
	assert.Equal(t, "none", got.AttestationType)
	assert.Equal(t, uint32(42), got.Authenticator.SignCount)
	assert.True(t, got.Flags.UserPresent)
	assert.True(t, got.Flags.UserVerified)
	assert.Equal(t, protocol.AuthenticatorAttachment("platform"), got.Authenticator.Attachment)
	assert.ElementsMatch(t,
		[]protocol.AuthenticatorTransport{protocol.USB, protocol.Internal},
		got.Transport,
	)
}

func TestWebAuthnCredentials_NilCredentialReturnsNil(t *testing.T) {
	wu := NewWebAuthnUser(&model.User{Id: 1}, nil)
	assert.Nil(t, wu.WebAuthnCredentials())
}

func TestWebAuthnCredentials_NilReceiver(t *testing.T) {
	var wu *WebAuthnUser
	assert.Nil(t, wu.WebAuthnCredentials())
}

// --- ModelUser / PasskeyCredential -----------------------------------------

func TestModelUser_And_PasskeyCredential_NilReceiver(t *testing.T) {
	var wu *WebAuthnUser
	assert.Nil(t, wu.ModelUser())
	assert.Nil(t, wu.PasskeyCredential())
}
