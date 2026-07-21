package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	casbinmodel "github.com/casbin/casbin/v2/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func countRules(t *testing.T, a *gormAdapter) int64 {
	t.Helper()
	var n int64
	require.NoError(t, a.db.Model(&model.CasbinRule{}).Count(&n).Error)
	return n
}

// AddPolicy inserts a rule and is idempotent (the count>0 early return).
func TestAdapter_AddPolicy_Idempotent(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	rule := []string{UserSubject(55), ResourceChannel, ActionSensitiveWrite, EffectAllow}

	require.NoError(t, a.AddPolicy("p", "p", rule))
	require.NoError(t, a.AddPolicy("p", "p", rule))

	assert.Equal(t, int64(1), countRules(t, a))
}

// AddPolicy with a short rule (fewer than 6 values) still works; the remaining
// v-columns stay empty.
func TestAdapter_AddPolicy_ShortRule(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	require.NoError(t, a.AddPolicy("g", "g", []string{"role:admin", "channel"}))

	var rule model.CasbinRule
	require.NoError(t, a.db.First(&rule).Error)
	assert.Equal(t, "g", rule.Ptype)
	assert.Equal(t, "role:admin", rule.V0)
	assert.Equal(t, "channel", rule.V1)
	assert.Equal(t, "", rule.V2)
}

// RemovePolicy deletes exactly the matching rule.
func TestAdapter_RemovePolicy(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	ruleA := []string{UserSubject(1), ResourceChannel, ActionRead, EffectAllow}
	ruleB := []string{UserSubject(2), ResourceChannel, ActionRead, EffectAllow}
	require.NoError(t, a.AddPolicy("p", "p", ruleA))
	require.NoError(t, a.AddPolicy("p", "p", ruleB))
	require.Equal(t, int64(2), countRules(t, a))

	require.NoError(t, a.RemovePolicy("p", "p", ruleA))
	assert.Equal(t, int64(1), countRules(t, a))

	var remaining model.CasbinRule
	require.NoError(t, a.db.First(&remaining).Error)
	assert.Equal(t, UserSubject(2), remaining.V0)
}

// RemoveFilteredPolicy at fieldIndex 0 removes all rows for a subject; an empty
// filter value is skipped (not added as a predicate).
func TestAdapter_RemoveFilteredPolicy_BySubject(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	require.NoError(t, a.AddPolicy("p", "p", []string{UserSubject(9), ResourceChannel, ActionRead, EffectAllow}))
	require.NoError(t, a.AddPolicy("p", "p", []string{UserSubject(9), ResourceChannel, ActionWrite, EffectDeny}))
	require.NoError(t, a.AddPolicy("p", "p", []string{UserSubject(10), ResourceChannel, ActionRead, EffectAllow}))

	require.NoError(t, a.RemoveFilteredPolicy("p", "p", 0, UserSubject(9)))
	assert.Equal(t, int64(1), countRules(t, a))

	var remaining model.CasbinRule
	require.NoError(t, a.db.First(&remaining).Error)
	assert.Equal(t, UserSubject(10), remaining.V0)
}

// RemoveFilteredPolicy with a resource filter at fieldIndex 1, and an empty
// trailing value (skipped) plus a non-empty one.
func TestAdapter_RemoveFilteredPolicy_MixedFilterValues(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	require.NoError(t, a.AddPolicy("p", "p", []string{UserSubject(9), ResourceChannel, ActionRead, EffectAllow}))
	require.NoError(t, a.AddPolicy("p", "p", []string{UserSubject(9), "other", ActionRead, EffectAllow}))

	// Filter v1="channel" (index 1), v2="" (skipped): removes only the channel row.
	require.NoError(t, a.RemoveFilteredPolicy("p", "p", 1, ResourceChannel, ""))
	assert.Equal(t, int64(1), countRules(t, a))
	var remaining model.CasbinRule
	require.NoError(t, a.db.First(&remaining).Error)
	assert.Equal(t, "other", remaining.V1)
}

// LoadPolicy round-trips rows into a casbin model, including the ruleToLine
// backfill of an empty effect column to "allow".
func TestAdapter_LoadPolicy_RoundTrip(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	// A p-rule missing its effect (V3 empty) — ruleToLine must backfill allow.
	require.NoError(t, a.db.Create(&model.CasbinRule{
		Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: ResourceChannel, V2: ActionRead,
	}).Error)
	// A p-rule with explicit deny.
	require.NoError(t, a.db.Create(&model.CasbinRule{
		Ptype: "p", V0: UserSubject(5), V1: ResourceChannel, V2: ActionWrite, V3: EffectDeny,
	}).Error)

	m, err := casbinmodel.NewModelFromString(modelText)
	require.NoError(t, err)
	require.NoError(t, a.LoadPolicy(m))

	hasAllow, err := m.HasPolicy("p", "p", []string{RoleSubject(BuiltInRoleAdmin), ResourceChannel, ActionRead, EffectAllow})
	require.NoError(t, err)
	assert.True(t, hasAllow, "empty effect column must be backfilled to allow")
	hasDeny, err := m.HasPolicy("p", "p", []string{UserSubject(5), ResourceChannel, ActionWrite, EffectDeny})
	require.NoError(t, err)
	assert.True(t, hasDeny)
}

// LoadPolicy surfaces a DB error (query against a dropped table).
func TestAdapter_LoadPolicy_DBError(t *testing.T) {
	db := newMigratedDB(t)
	require.NoError(t, db.Migrator().DropTable(&model.CasbinRule{}))
	a := newGormAdapter(db)

	m, err := casbinmodel.NewModelFromString(modelText)
	require.NoError(t, err)
	assert.Error(t, a.LoadPolicy(m))
}

// SavePolicy wipes the table and re-writes every p and g policy from the model.
func TestAdapter_SavePolicy_ReplacesAll(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	// Pre-existing stale row that must be wiped.
	require.NoError(t, a.db.Create(&model.CasbinRule{Ptype: "p", V0: "stale"}).Error)

	m, err := casbinmodel.NewModelFromString(modelText)
	require.NoError(t, err)
	m.AddPolicy("p", "p", []string{RoleSubject(BuiltInRoleAdmin), ResourceChannel, ActionRead, EffectAllow})
	m.AddPolicy("p", "p", []string{UserSubject(3), ResourceChannel, ActionWrite, EffectDeny})

	require.NoError(t, a.SavePolicy(m))

	assert.Equal(t, int64(2), countRules(t, a))
	var stale int64
	require.NoError(t, a.db.Model(&model.CasbinRule{}).Where("v0 = ?", "stale").Count(&stale).Error)
	assert.Equal(t, int64(0), stale)
}

// SavePolicy also persists grouping ("g") policies, not just "p" policies.
func TestAdapter_SavePolicy_PersistsGroupingPolicies(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))

	// A model that supports both p and g sections so AddPolicy populates m["g"].
	const modelWithRoles = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act, eft
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`
	m, err := casbinmodel.NewModelFromString(modelWithRoles)
	require.NoError(t, err)
	m.AddPolicy("p", "p", []string{RoleSubject(BuiltInRoleAdmin), ResourceChannel, ActionRead, EffectAllow})
	m.AddPolicy("g", "g", []string{UserSubject(1), RoleSubject(BuiltInRoleAdmin)})

	require.NoError(t, a.SavePolicy(m))

	var gRows int64
	require.NoError(t, a.db.Model(&model.CasbinRule{}).Where("ptype = ?", "g").Count(&gRows).Error)
	assert.Equal(t, int64(1), gRows, "grouping policy must be persisted")
	var pRows int64
	require.NoError(t, a.db.Model(&model.CasbinRule{}).Where("ptype = ?", "p").Count(&pRows).Error)
	assert.Equal(t, int64(1), pRows)
}

// SavePolicy with an empty model still wipes existing rows and inserts nothing
// (the len(rules)==0 early return).
func TestAdapter_SavePolicy_EmptyModelWipes(t *testing.T) {
	a := newGormAdapter(newMigratedDB(t))
	require.NoError(t, a.db.Create(&model.CasbinRule{Ptype: "p", V0: "stale"}).Error)

	m, err := casbinmodel.NewModelFromString(modelText)
	require.NoError(t, err)
	require.NoError(t, a.SavePolicy(m))

	assert.Equal(t, int64(0), countRules(t, a))
}

// ---------------------------------------------------------------------------
// newRule / ruleToLine — pure helpers.
// ---------------------------------------------------------------------------

// newRule maps up to 6 policy values into V0..V5 and ignores any extra values.
func TestNewRule_MapsUpToSixValuesAndTruncates(t *testing.T) {
	r := newRule("p", []string{"a", "b", "c", "d", "e", "f", "OVERFLOW", "OVERFLOW2"})
	assert.Equal(t, "p", r.Ptype)
	assert.Equal(t, "a", r.V0)
	assert.Equal(t, "b", r.V1)
	assert.Equal(t, "c", r.V2)
	assert.Equal(t, "d", r.V3)
	assert.Equal(t, "e", r.V4)
	assert.Equal(t, "f", r.V5)
}

func TestNewRule_ShortPolicyLeavesTrailingEmpty(t *testing.T) {
	r := newRule("g", []string{"role:admin"})
	assert.Equal(t, "role:admin", r.V0)
	assert.Equal(t, "", r.V1)
	assert.Equal(t, "", r.V5)
}

// ruleToLine: a p-rule with a complete sub/obj/act triple but an empty effect
// (V3) is backfilled to "allow"; empty trailing columns are dropped.
func TestRuleToLine_BackfillsAllowForLegacyPRule(t *testing.T) {
	line := ruleToLine(model.CasbinRule{
		Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: ResourceChannel, V2: ActionRead,
	})
	assert.Equal(t, "p, role:admin, channel, read, allow", line)
}

// A p-rule that already has an explicit effect is left untouched.
func TestRuleToLine_KeepsExplicitEffect(t *testing.T) {
	line := ruleToLine(model.CasbinRule{
		Ptype: "p", V0: UserSubject(5), V1: ResourceChannel, V2: ActionWrite, V3: EffectDeny,
	})
	assert.Equal(t, "p, user:5, channel, write, deny", line)
}

// A g-rule (grouping) is not subject to the p-rule effect backfill.
func TestRuleToLine_GroupingRuleNotBackfilled(t *testing.T) {
	line := ruleToLine(model.CasbinRule{Ptype: "g", V0: "user:1", V1: "role:admin"})
	assert.Equal(t, "g, user:1, role:admin", line)
}

// A p-rule that is incomplete (missing act) does not trigger the backfill.
func TestRuleToLine_IncompletePRuleNoBackfill(t *testing.T) {
	line := ruleToLine(model.CasbinRule{Ptype: "p", V0: "user:1", V1: "channel"})
	assert.Equal(t, "p, user:1, channel", line)
}
