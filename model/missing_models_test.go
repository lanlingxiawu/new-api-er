package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetMissingModels returns enabled ability models that have no row in the
// models metadata table.
func TestGetMissingModels(t *testing.T) {
	requireDB(t)

	withMeta := uniq("zzhasmeta")
	withoutMeta := uniq("zznometa")

	// Publish both as enabled abilities.
	ch := mkChannel(t, func(c *Channel) {
		c.Group = uniq("mgrp")
		c.Models = withMeta + "," + withoutMeta
		c.Status = common.ChannelStatusEnabled
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)

	// Give only one of them a metadata row.
	m := &Model{ModelName: withMeta, Status: 1, NameRule: NameRuleExact}
	require.NoError(t, m.Insert())
	deleteByID(t, &Model{}, m.Id)

	missing, err := GetMissingModels()
	require.NoError(t, err)

	assert.Contains(t, missing, withoutMeta, "model without metadata is missing")
	assert.NotContains(t, missing, withMeta, "model with metadata is not missing")
}
