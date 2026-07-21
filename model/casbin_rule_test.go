package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// casbin_rule.go is a pure struct + TableName declaration (no query logic).
func TestCasbinRule_TableName(t *testing.T) {
	assert.Equal(t, "casbin_rule", CasbinRule{}.TableName())
}
