package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// system_task.go: input-validation guards on the async task management endpoints.

func TestCreateLogCleanupSystemTask_MissingTimestamp(t *testing.T) {
	ctx, rec := newCtx(t, "POST", "/api/system-task/log-cleanup", nil)
	CreateLogCleanupSystemTask(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestGetCurrentSystemTask_MissingType(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/system-task/current", nil)
	GetCurrentSystemTask(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestGetSystemTask_MissingTaskId(t *testing.T) {
	// task_id path param empty -> "task id is required".
	ctx, rec := newCtx(t, "GET", "/api/system-task/", nil)
	GetSystemTask(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestListSystemTasks_And_CurrentByType_Return200(t *testing.T) {
	requireDB(t)
	// Ensure the backing table exists so the query path executes cleanly.
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}))

	ctx, rec := newCtx(t, "GET", "/api/system-task?limit=5", nil)
	ListSystemTasks(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)

	// A type with no active task returns success with null data.
	ctx2, rec2 := newCtx(t, "GET", "/api/system-task/current?type=log_cleanup", nil)
	GetCurrentSystemTask(ctx2)
	resp2 := decodeResp(t, rec2)
	assert.True(t, resp2.Success)
}
