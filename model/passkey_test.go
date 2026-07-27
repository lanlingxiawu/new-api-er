package model

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Pure logic: transport (de)serialisation + credential conversion helpers
// ---------------------------------------------------------------------------

func TestPasskey_TransportRoundTrip(t *testing.T) {
	p := &PasskeyCredential{}

	// nil / empty -> nil list
	assert.Nil(t, p.TransportList())

	// empty list clears field
	p.Transports = "junk"
	p.SetTransports(nil)
	assert.Equal(t, "", p.Transports)
	assert.Nil(t, p.TransportList())

	// round trip a real list
	in := []protocol.AuthenticatorTransport{protocol.USB, protocol.Internal}
	p.SetTransports(in)
	assert.NotEmpty(t, p.Transports)
	assert.Equal(t, in, p.TransportList())

	// malformed JSON -> nil (no panic)
	p.Transports = "{not-json"
	assert.Nil(t, p.TransportList())
}

func TestPasskey_NilTransportListReceiver(t *testing.T) {
	var p *PasskeyCredential
	assert.Nil(t, p.TransportList())
}

func TestPasskey_FromWebAuthnAndBack(t *testing.T) {
	// nil credential -> nil
	assert.Nil(t, NewPasskeyCredentialFromWebAuthn(1, nil))

	cred := &webauthn.Credential{
		ID:              []byte("credential-id-bytes"),
		PublicKey:       []byte("public-key-bytes"),
		AttestationType: "packed",
		Transport:       []protocol.AuthenticatorTransport{protocol.USB},
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: true,
			BackupState:    false,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:       []byte("aaguid-bytes"),
			SignCount:    7,
			CloneWarning: false,
			Attachment:   protocol.Platform,
		},
	}

	pk := NewPasskeyCredentialFromWebAuthn(42, cred)
	require.NotNil(t, pk)
	assert.Equal(t, 42, pk.UserID)
	assert.Equal(t, base64.StdEncoding.EncodeToString(cred.ID), pk.CredentialID)
	assert.Equal(t, base64.StdEncoding.EncodeToString(cred.PublicKey), pk.PublicKey)
	assert.Equal(t, "packed", pk.AttestationType)
	assert.EqualValues(t, 7, pk.SignCount)
	assert.True(t, pk.UserPresent)
	assert.True(t, pk.UserVerified)
	assert.True(t, pk.BackupEligible)
	assert.False(t, pk.BackupState)
	assert.Equal(t, string(protocol.Platform), pk.Attachment)

	// Convert back -> byte fields must decode to the originals.
	back := pk.ToWebAuthnCredential()
	assert.Equal(t, cred.ID, back.ID)
	assert.Equal(t, cred.PublicKey, back.PublicKey)
	assert.Equal(t, cred.Authenticator.AAGUID, back.Authenticator.AAGUID)
	assert.EqualValues(t, 7, back.Authenticator.SignCount)
	assert.Equal(t, "packed", back.AttestationType)
	assert.True(t, back.Flags.UserVerified)
	assert.Equal(t, cred.Transport, back.Transport)
}

// ---------------------------------------------------------------------------
// DB: CRUD, per-user lookup, counter update, delete
// ---------------------------------------------------------------------------

func pkMake(t *testing.T, userID int, signCount uint32) *PasskeyCredential {
	t.Helper()
	requireDB(t)
	cred := &PasskeyCredential{
		UserID:          userID,
		CredentialID:    base64.StdEncoding.EncodeToString([]byte(uniq("credid"))),
		PublicKey:       base64.StdEncoding.EncodeToString([]byte(uniq("pub"))),
		AttestationType: "none",
		SignCount:       signCount,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, DB.Create(cred).Error)
	deleteByID(t, &PasskeyCredential{}, cred.ID)
	return cred
}

func TestPasskey_GetByUserID(t *testing.T) {
	// 0 -> friendly error
	_, err := GetPasskeyByUserID(0)
	assert.ErrorIs(t, err, ErrFriendlyPasskeyNotFound)

	u := mkUser(t, nil)

	// not bound -> ErrPasskeyNotFound
	_, err = GetPasskeyByUserID(u.Id)
	assert.ErrorIs(t, err, ErrPasskeyNotFound)

	cred := pkMake(t, u.Id, 3)
	got, err := GetPasskeyByUserID(u.Id)
	require.NoError(t, err)
	assert.Equal(t, cred.ID, got.ID)
	assert.EqualValues(t, 3, got.SignCount)
}

func TestPasskey_GetByCredentialID(t *testing.T) {
	// empty -> friendly error
	_, err := GetPasskeyByCredentialID(nil)
	assert.ErrorIs(t, err, ErrFriendlyPasskeyNotFound)

	// unknown credential -> friendly error
	_, err = GetPasskeyByCredentialID([]byte("does-not-exist-cred"))
	assert.ErrorIs(t, err, ErrFriendlyPasskeyNotFound)

	u := mkUser(t, nil)
	rawID := []byte(uniq("rawcred"))
	cred := &PasskeyCredential{
		UserID:       u.Id,
		CredentialID: base64.StdEncoding.EncodeToString(rawID),
		PublicKey:    base64.StdEncoding.EncodeToString([]byte("pub")),
	}
	require.NoError(t, DB.Create(cred).Error)
	deleteByID(t, &PasskeyCredential{}, cred.ID)

	got, err := GetPasskeyByCredentialID(rawID)
	require.NoError(t, err)
	assert.Equal(t, cred.ID, got.ID)
	assert.Equal(t, u.Id, got.UserID)
}

func TestPasskey_UpsertCreatesAndReplaces(t *testing.T) {
	// nil credential -> error
	assert.Error(t, UpsertPasskeyCredentialWithAuthVersion(nil))

	u := mkUser(t, nil)
	first := &PasskeyCredential{
		UserID:       u.Id,
		CredentialID: base64.StdEncoding.EncodeToString([]byte(uniq("c1"))),
		PublicKey:    base64.StdEncoding.EncodeToString([]byte("p1")),
		SignCount:    1,
	}
	require.NoError(t, UpsertPasskeyCredentialWithAuthVersion(first))
	t.Cleanup(func() { _ = DeletePasskeyByUserIDWithAuthVersion(u.Id) })

	got, err := GetPasskeyByUserID(u.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.SignCount)

	// upsert again for same user replaces the row (delete + create).
	second := &PasskeyCredential{
		UserID:       u.Id,
		CredentialID: base64.StdEncoding.EncodeToString([]byte(uniq("c2"))),
		PublicKey:    base64.StdEncoding.EncodeToString([]byte("p2")),
		SignCount:    2,
	}
	require.NoError(t, UpsertPasskeyCredentialWithAuthVersion(second))

	got, err = GetPasskeyByUserID(u.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 2, got.SignCount)
	assert.Equal(t, second.CredentialID, got.CredentialID)

	// exactly one row for the user
	var count int64
	require.NoError(t, DB.Model(&PasskeyCredential{}).Where("user_id = ?", u.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestPasskey_SignCounterUpdateViaUpsert(t *testing.T) {
	u := mkUser(t, nil)
	rawID := []byte(uniq("counter"))
	cred := NewPasskeyCredentialFromWebAuthn(u.Id, &webauthn.Credential{
		ID:        rawID,
		PublicKey: []byte("pub"),
		Authenticator: webauthn.Authenticator{
			SignCount: 10,
		},
	})
	require.NoError(t, UpsertPasskeyCredentialWithAuthVersion(cred))
	t.Cleanup(func() { _ = DeletePasskeyByUserIDWithAuthVersion(u.Id) })

	// simulate a validated assertion advancing the sign counter
	cred.SignCount = 11
	require.NoError(t, UpsertPasskeyCredentialWithAuthVersion(cred))

	got, err := GetPasskeyByCredentialID(rawID)
	require.NoError(t, err)
	assert.EqualValues(t, 11, got.SignCount)
}

func TestPasskey_DeleteByUserID(t *testing.T) {
	// 0 -> error
	assert.Error(t, DeletePasskeyByUserIDWithAuthVersion(0))

	u := mkUser(t, nil)
	pkMake(t, u.Id, 1)

	require.NoError(t, DeletePasskeyByUserIDWithAuthVersion(u.Id))
	_, err := GetPasskeyByUserID(u.Id)
	assert.ErrorIs(t, err, ErrPasskeyNotFound)

	// deleting again errors: upstream #6329 变体在找不到凭据时返回 ErrPasskeyNotFound
	assert.ErrorIs(t, DeletePasskeyByUserIDWithAuthVersion(u.Id), ErrPasskeyNotFound)
}

// sanity: the two passkey sentinel errors are distinct.
func TestPasskey_SentinelErrorsDistinct(t *testing.T) {
	assert.NotErrorIs(t, ErrPasskeyNotFound, ErrFriendlyPasskeyNotFound)
	assert.NotErrorIs(t, ErrPasskeyNotFound, gorm.ErrRecordNotFound)
}
