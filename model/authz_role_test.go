package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// authz_role.go is a pure struct + TableName declaration (no query logic, and
// the model is not registered in AutoMigrate). Only the table mapping carries
// behavior worth pinning.
func TestAuthzRole_TableName(t *testing.T) {
	assert.Equal(t, "authz_roles", AuthzRole{}.TableName())
}
