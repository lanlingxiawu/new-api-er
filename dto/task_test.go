package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TaskResponse 仍属主模块 dto（任务类 DTO 未随 relaykit 拆分迁出）。
func TestTaskResponse_IsSuccess(t *testing.T) {
	assert.True(t, (&TaskResponse[string]{Code: TaskSuccessCode}).IsSuccess())
	assert.False(t, (&TaskResponse[string]{Code: "failed"}).IsSuccess())
}
