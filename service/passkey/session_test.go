package passkey

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSessionContext returns a gin.Context that already has the sessions
// middleware applied (cookie store). Values Set within the request are
// retained in memory, so a Save+Pop round-trip works inside one context.
func newSessionContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	store := cookie.NewStore([]byte("passkey-test-secret"))
	sessions.Sessions("passkeytest", store)(c) // runs the middleware, sets DefaultKey
	return c
}

func sampleSessionData() *webauthn.SessionData {
	return &webauthn.SessionData{
		Challenge:            "challenge-abc",
		RelyingPartyID:       "example.com",
		UserID:               []byte("42"),
		AllowedCredentialIDs: [][]byte{[]byte("cred-1")},
		Expires:              time.Now().Add(2 * time.Minute).UTC().Truncate(time.Second),
		UserVerification:     "preferred",
	}
}

// --- SaveSessionData + PopSessionData round trip ---------------------------

func TestSaveAndPopSessionData_RoundTrip(t *testing.T) {
	c := newSessionContext(t)
	want := sampleSessionData()

	require.NoError(t, SaveSessionData(c, RegistrationSessionKey, want))

	got, err := PopSessionData(c, RegistrationSessionKey)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, want.Challenge, got.Challenge)
	assert.Equal(t, want.RelyingPartyID, got.RelyingPartyID)
	assert.Equal(t, want.UserID, got.UserID)
	assert.Equal(t, want.AllowedCredentialIDs, got.AllowedCredentialIDs)
	assert.Equal(t, want.UserVerification, got.UserVerification)
	assert.WithinDuration(t, want.Expires, got.Expires, time.Second)
}

func TestPopSessionData_ConsumesKey(t *testing.T) {
	c := newSessionContext(t)
	require.NoError(t, SaveSessionData(c, LoginSessionKey, sampleSessionData()))

	_, err := PopSessionData(c, LoginSessionKey)
	require.NoError(t, err)

	// Second pop must fail: key was deleted on first pop.
	_, err = PopSessionData(c, LoginSessionKey)
	assert.ErrorIs(t, err, errSessionNotFound)
}

// --- SaveSessionData with nil data => delete -------------------------------

func TestSaveSessionData_NilDataDeletesKey(t *testing.T) {
	c := newSessionContext(t)
	require.NoError(t, SaveSessionData(c, VerifySessionKey, sampleSessionData()))

	// Saving nil deletes the stored key.
	require.NoError(t, SaveSessionData(c, VerifySessionKey, nil))

	_, err := PopSessionData(c, VerifySessionKey)
	assert.ErrorIs(t, err, errSessionNotFound)
}

func TestSaveSessionData_NilDataOnMissingKeyIsNoError(t *testing.T) {
	c := newSessionContext(t)
	// Deleting a key that was never set must not error.
	assert.NoError(t, SaveSessionData(c, "never-set", nil))
}

// --- PopSessionData error paths -------------------------------------------

func TestPopSessionData_MissingKeyReturnsNotFound(t *testing.T) {
	c := newSessionContext(t)
	got, err := PopSessionData(c, RegistrationSessionKey)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, errSessionNotFound)
}

func TestPopSessionData_ByteSlicePayload(t *testing.T) {
	c := newSessionContext(t)
	payload, err := json.Marshal(sampleSessionData())
	require.NoError(t, err)

	// Store raw []byte directly (mimics a store that returns []byte).
	sess := sessions.Default(c)
	sess.Set(RegistrationSessionKey, payload)
	require.NoError(t, sess.Save())

	got, err := PopSessionData(c, RegistrationSessionKey)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "challenge-abc", got.Challenge)
}

func TestPopSessionData_MalformedStringPayload(t *testing.T) {
	c := newSessionContext(t)
	sess := sessions.Default(c)
	sess.Set(LoginSessionKey, "this-is-not-json")
	require.NoError(t, sess.Save())

	got, err := PopSessionData(c, LoginSessionKey)
	assert.Nil(t, got)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errSessionNotFound) // JSON error, not the not-found sentinel
}

func TestPopSessionData_MalformedBytePayload(t *testing.T) {
	c := newSessionContext(t)
	sess := sessions.Default(c)
	sess.Set(VerifySessionKey, []byte("{not valid json"))
	require.NoError(t, sess.Save())

	got, err := PopSessionData(c, VerifySessionKey)
	assert.Nil(t, got)
	require.Error(t, err)
}

func TestPopSessionData_UnsupportedTypeReturnsInvalidFormat(t *testing.T) {
	c := newSessionContext(t)
	sess := sessions.Default(c)
	sess.Set(RegistrationSessionKey, 12345) // int => default branch
	require.NoError(t, sess.Save())

	got, err := PopSessionData(c, RegistrationSessionKey)
	assert.Nil(t, got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "格式无效")
}

func TestSessionKeyConstants_AreDistinct(t *testing.T) {
	keys := map[string]struct{}{
		RegistrationSessionKey: {},
		LoginSessionKey:        {},
		VerifySessionKey:       {},
	}
	assert.Len(t, keys, 3, "session keys must be unique")
}
