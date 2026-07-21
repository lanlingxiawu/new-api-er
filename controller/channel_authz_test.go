package controller

import (
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// channelHasSensitiveChanges: precise old-vs-new comparison for each gated
// sensitive field, plus the fail-closed scan for unclassified fields.
// Technique: decision + condition coverage (each `ok && changed` sub-condition),
// equivalence partitioning (sensitive vs non-sensitive vs unknown fields).
// ---------------------------------------------------------------------------

func chanAuthzOrigin() *model.Channel {
	baseURL := "https://api.example.com"
	org := "org-orig"
	header := `{"Authorization":"Bearer {api_key}"}`
	param := `{"temperature":0}`
	setting := `{"a":1}`
	return &model.Channel{
		Type:               1,
		Key:                "old-key",
		BaseURL:            &baseURL,
		OpenAIOrganization: &org,
		HeaderOverride:     &header,
		ParamOverride:      &param,
		Setting:            &setting,
		Other:              "other-orig",
		OtherSettings:      "settings-orig",
		Models:             "gpt-4o",
		Group:              "default",
	}
}

func TestChannelHasSensitiveChanges_NonSensitiveRoutingFields(t *testing.T) {
	origin := chanAuthzOrigin()
	updated := PatchChannel{Channel: *origin}
	updated.Models = "gpt-4o,gpt-4o-mini"
	updated.Group = "vip"
	assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
		"models": updated.Models,
		"group":  updated.Group,
	}))
}

func TestChannelHasSensitiveChanges_EachSensitiveFieldTrips(t *testing.T) {
	newBase := "https://leak.example.com"
	newOrg := "org-new"
	newHeader := `{"X-Key":"{api_key}"}`
	newParam := `{"temperature":1}`
	newSetting := `{"a":2}`

	cases := []struct {
		name    string
		field   string
		mutate  func(p *PatchChannel)
		reqData map[string]any
	}{
		{"type", "type", func(p *PatchChannel) { p.Type = 2 }, map[string]any{"type": 2}},
		{"key", "key", func(p *PatchChannel) { p.Key = "new-key" }, map[string]any{"key": "new-key"}},
		{"base_url", "base_url", func(p *PatchChannel) { p.BaseURL = &newBase }, map[string]any{"base_url": newBase}},
		{"openai_organization", "openai_organization", func(p *PatchChannel) { p.OpenAIOrganization = &newOrg }, map[string]any{"openai_organization": newOrg}},
		{"header_override", "header_override", func(p *PatchChannel) { p.HeaderOverride = &newHeader }, map[string]any{"header_override": newHeader}},
		{"param_override", "param_override", func(p *PatchChannel) { p.ParamOverride = &newParam }, map[string]any{"param_override": newParam}},
		{"setting", "setting", func(p *PatchChannel) { p.Setting = &newSetting }, map[string]any{"setting": newSetting}},
		{"other", "other", func(p *PatchChannel) { p.Other = "other-new" }, map[string]any{"other": "other-new"}},
		{"settings", "settings", func(p *PatchChannel) { p.OtherSettings = "settings-new" }, map[string]any{"settings": "settings-new"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origin := chanAuthzOrigin()
			updated := PatchChannel{Channel: *origin}
			tc.mutate(&updated)
			assert.True(t, channelHasSensitiveChanges(&updated, origin, tc.reqData))
		})
	}
}

func TestChannelHasSensitiveChanges_KeyModePresenceTrips(t *testing.T) {
	origin := chanAuthzOrigin()
	updated := PatchChannel{Channel: *origin}
	mode := "append"
	updated.KeyMode = &mode
	// key_mode is sensitive purely by presence of a non-nil pointer.
	assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"key_mode": "append"}))
}

func TestChannelHasSensitiveChanges_KeyModeNilDoesNotTrip(t *testing.T) {
	origin := chanAuthzOrigin()
	updated := PatchChannel{Channel: *origin}
	updated.KeyMode = nil
	assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"key_mode": nil}))
}

func TestChannelHasSensitiveChanges_EmptyKeyNotTreatedAsChange(t *testing.T) {
	// Condition coverage: key present, but channel.Key == "" -> the second
	// sub-condition (channel.Key != "") is false, so no sensitive change.
	origin := chanAuthzOrigin()
	updated := PatchChannel{Channel: *origin}
	updated.Key = ""
	assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"key": ""}))
}

func TestChannelHasSensitiveChanges_SameValueNotTreatedAsChange(t *testing.T) {
	// field present in request but value identical to origin -> not sensitive.
	origin := chanAuthzOrigin()
	updated := PatchChannel{Channel: *origin}
	assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
		"type":     origin.Type,
		"base_url": *origin.BaseURL,
		"other":    origin.Other,
	}))
}

func TestChannelHasSensitiveChanges_OmittedSensitiveFieldsIgnored(t *testing.T) {
	// A sensitive field changed in the struct but absent from requestData must
	// not trip (the `_, ok := requestData[...]` gate is false).
	origin := chanAuthzOrigin()
	updated := PatchChannel{}
	updated.Id = origin.Id
	updated.Priority = origin.Priority
	assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"priority": 10}))
}

func TestChannelHasSensitiveChanges_UnknownFieldFailsClosed(t *testing.T) {
	origin := chanAuthzOrigin()
	updated := PatchChannel{Channel: *origin}
	assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"future_secret_field": "x"}))
}

func TestChannelHasSensitiveChanges_OperationalAndReadOnlyIgnored(t *testing.T) {
	origin := chanAuthzOrigin()
	updated := PatchChannel{Channel: *origin}
	updated.Status = common.ChannelStatusManuallyDisabled
	updated.Balance = 99
	updated.UsedQuota = 100
	updated.ResponseTime = 200
	assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
		"status":        updated.Status,
		"balance":       updated.Balance,
		"used_quota":    updated.UsedQuota,
		"response_time": updated.ResponseTime,
	}))
}

// ---------------------------------------------------------------------------
// clearChannelReadOnlyFields: only fields present in requestData are zeroed.
// ---------------------------------------------------------------------------

func TestClearChannelReadOnlyFields_ClearsOnlyPresentFields(t *testing.T) {
	channel := PatchChannel{Channel: model.Channel{
		CreatedTime:        11,
		TestTime:           22,
		ResponseTime:       33,
		Balance:            44.5,
		BalanceUpdatedTime: 55,
		UsedQuota:          66,
		Models:             "gpt-4o",
		Group:              "default",
	}}
	clearChannelReadOnlyFields(&channel, map[string]any{
		"created_time":         channel.CreatedTime,
		"test_time":            channel.TestTime,
		"response_time":        channel.ResponseTime,
		"balance":              channel.Balance,
		"balance_updated_time": channel.BalanceUpdatedTime,
		"used_quota":           channel.UsedQuota,
		"models":               channel.Models,
		"group":                channel.Group,
	})
	assert.Zero(t, channel.CreatedTime)
	assert.Zero(t, channel.TestTime)
	assert.Zero(t, channel.ResponseTime)
	assert.Zero(t, channel.Balance)
	assert.Zero(t, channel.BalanceUpdatedTime)
	assert.Zero(t, channel.UsedQuota)
	// non read-only fields untouched
	assert.Equal(t, "gpt-4o", channel.Models)
	assert.Equal(t, "default", channel.Group)
}

func TestClearChannelReadOnlyFields_AbsentFieldsUntouched(t *testing.T) {
	channel := PatchChannel{Channel: model.Channel{CreatedTime: 11, Balance: 44.5}}
	// requestData only mentions created_time; balance must be preserved.
	clearChannelReadOnlyFields(&channel, map[string]any{"created_time": int64(11)})
	assert.Zero(t, channel.CreatedTime)
	assert.Equal(t, 44.5, channel.Balance)
}

// ---------------------------------------------------------------------------
// isManageableChannelStatus: boundary/equivalence over the status enum.
// ---------------------------------------------------------------------------

func TestIsManageableChannelStatus(t *testing.T) {
	assert.True(t, isManageableChannelStatus(common.ChannelStatusEnabled))
	assert.True(t, isManageableChannelStatus(common.ChannelStatusManuallyDisabled))
	assert.False(t, isManageableChannelStatus(common.ChannelStatusAutoDisabled))
	assert.False(t, isManageableChannelStatus(0))
	assert.False(t, isManageableChannelStatus(999))
}

// ---------------------------------------------------------------------------
// equalStringPtr: nil/non-nil combinations (path coverage).
// ---------------------------------------------------------------------------

func TestEqualStringPtr(t *testing.T) {
	s1, s2, s3 := "a", "a", "b"
	assert.True(t, equalStringPtr(nil, nil))
	assert.False(t, equalStringPtr(&s1, nil))
	assert.False(t, equalStringPtr(nil, &s1))
	assert.True(t, equalStringPtr(&s1, &s2))
	assert.False(t, equalStringPtr(&s1, &s3))
}

// ---------------------------------------------------------------------------
// Guard: every JSON field of PatchChannel must be classified into exactly one
// of the four sets, so a newly added field forces a conscious permission
// decision instead of silently defaulting.
// ---------------------------------------------------------------------------

func TestChannelFieldsAreClassified(t *testing.T) {
	classified := func(name string) bool {
		if _, ok := channelSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelNonSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelOperationalFields[name]; ok {
			return true
		}
		_, ok := channelReadOnlyFields[name]
		return ok
	}

	var collect func(rt reflect.Type) []string
	collect = func(rt reflect.Type) []string {
		var names []string
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				names = append(names, collect(field.Type)...)
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			names = append(names, name)
		}
		return names
	}

	for _, name := range collect(reflect.TypeOf(PatchChannel{})) {
		assert.Truef(t, classified(name),
			"channel field %q is not classified; add it to channelSensitiveFields, channelNonSensitiveFields, channelOperationalFields, or channelReadOnlyFields in channel_authz.go", name)
	}
}
