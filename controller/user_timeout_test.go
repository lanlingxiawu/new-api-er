package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setAllUserTimeouts(user *model.User, value int) {
	user.StreamResponseTimeout = value
	user.StreamTotalTimeout = value
	user.NonStreamResponseTimeout = value
	user.NonStreamTotalTimeout = value
}

func assertAllUserTimeouts(t *testing.T, user model.User, value int) {
	t.Helper()
	assert.Equal(t, value, user.StreamResponseTimeout)
	assert.Equal(t, value, user.StreamTotalTimeout)
	assert.Equal(t, value, user.NonStreamResponseTimeout)
	assert.Equal(t, value, user.NonStreamTotalTimeout)
}

func TestUpdateUserTimeoutsOmittedPreserveExistingValues(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(user *model.User) {
		setAllUserTimeouts(user, 120)
		user.StreamResponseTimeoutMode = service.RelayStreamResponseTimeoutModeIdle
	})
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
		"id": user.Id, "username": user.Username, "group": user.Group,
	})
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	require.True(t, decodeResp(t, rec).Success)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assertAllUserTimeouts(t, stored, 120)
	assert.Equal(t, service.RelayStreamResponseTimeoutModeIdle, stored.StreamResponseTimeoutMode)
}

func TestUpdateUserTimeoutsCanResetOneFieldToInheritance(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(user *model.User) { setAllUserTimeouts(user, 120) })
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
		"id": user.Id, "username": user.Username, "group": user.Group,
		"stream_response_timeout": 0,
	})
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	require.True(t, decodeResp(t, rec).Success)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Zero(t, stored.StreamResponseTimeout)
	assert.Equal(t, 120, stored.StreamTotalTimeout)
	assert.Equal(t, 120, stored.NonStreamResponseTimeout)
	assert.Equal(t, 120, stored.NonStreamTotalTimeout)
}

func TestUpdateUserTimeoutsMapsAllFourFieldsIndependently(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(user *model.User) { setAllUserTimeouts(user, 120) })
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
		"id":                          user.Id,
		"username":                    user.Username,
		"group":                       user.Group,
		"stream_response_timeout":     -1,
		"stream_total_timeout":        22,
		"non_stream_response_timeout": 33,
		"non_stream_total_timeout":    44,
	})
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	require.True(t, decodeResp(t, rec).Success)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, -1, stored.StreamResponseTimeout)
	assert.Equal(t, 22, stored.StreamTotalTimeout)
	assert.Equal(t, 33, stored.NonStreamResponseTimeout)
	assert.Equal(t, 44, stored.NonStreamTotalTimeout)
}

func TestUpdateUserTimeoutsCanUpdateStreamResponseMode(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(user *model.User) {
		setAllUserTimeouts(user, 120)
		user.StreamResponseTimeoutMode = service.RelayStreamResponseTimeoutModeFirstOutput
	})
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
		"id":                           user.Id,
		"username":                     user.Username,
		"group":                        user.Group,
		"stream_response_timeout_mode": service.RelayStreamResponseTimeoutModeIdle,
	})
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	require.True(t, decodeResp(t, rec).Success)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, service.RelayStreamResponseTimeoutModeIdle, stored.StreamResponseTimeoutMode)
	assertAllUserTimeouts(t, stored, 120)
}

func TestUpdateUserTimeoutsNormalizesStreamResponseMode(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(user *model.User) {
		setAllUserTimeouts(user, 120)
		user.StreamResponseTimeoutMode = service.RelayStreamResponseTimeoutModeFirstOutput
	})
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
		"id":                           user.Id,
		"username":                     user.Username,
		"group":                        user.Group,
		"stream_response_timeout_mode": " IDLE ",
	})
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	require.True(t, decodeResp(t, rec).Success)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, service.RelayStreamResponseTimeoutModeIdle, stored.StreamResponseTimeoutMode)
	assertAllUserTimeouts(t, stored, 120)
}

func TestUpdateUserTimeoutsRejectInvalidStreamResponseMode(t *testing.T) {
	requireDB(t)
	user := mkUser(t, func(user *model.User) {
		setAllUserTimeouts(user, 120)
		user.StreamResponseTimeoutMode = service.RelayStreamResponseTimeoutModeFirstOutput
	})
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
		"id":                           user.Id,
		"username":                     user.Username,
		"group":                        user.Group,
		"stream_response_timeout_mode": "unexpected",
	})
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	assert.False(t, decodeResp(t, rec).Success)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, service.RelayStreamResponseTimeoutModeFirstOutput, stored.StreamResponseTimeoutMode)
	assertAllUserTimeouts(t, stored, 120)
}

func TestUpdateUserTimeoutsRejectEveryOutOfRangeField(t *testing.T) {
	requireDB(t)
	fields := []string{
		"stream_response_timeout",
		"stream_total_timeout",
		"non_stream_response_timeout",
		"non_stream_total_timeout",
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			user := mkUser(t, func(user *model.User) { setAllUserTimeouts(user, 120) })
			ctx, rec := newCtx(t, http.MethodPut, "/api/user/", map[string]any{
				"id": user.Id, "username": user.Username, "group": user.Group,
				field: 604801,
			})
			asAdmin(ctx, nextTestID())
			UpdateUser(ctx)
			assert.False(t, decodeResp(t, rec).Success)

			var stored model.User
			require.NoError(t, model.DB.First(&stored, user.Id).Error)
			assertAllUserTimeouts(t, stored, 120)
		})
	}
}
