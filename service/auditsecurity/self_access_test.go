package auditsecurity

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSelfSecurityIsolationAndProjection(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	evidence, err := common.Marshal([]model.SecurityEvent{
		{EventID: "own", ActorID: 31, ActorRole: 1, Action: "login", At: now, IP: "192.0.2.31", Success: true, Facts: `{"internal":"private"}`},
		{EventID: "foreign", ActorID: 32, ActorRole: 1, Action: "login", IP: "192.0.2.32"},
		{EventID: "anonymous", ActorID: 31, ActorRole: 0, Action: "auth.login_failed", IP: "192.0.2.99"},
		{EventID: "reused", ActorID: 31, ActorRole: 0, Action: "auth.session_refresh_reuse", IP: "192.0.2.33"},
	})
	require.NoError(t, err)
	alert := model.SecurityAlert{ActorID: 31, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Visibility: 100, Status: "open", Version: 1, FirstSeen: now, LastSeen: now, Evidence: string(evidence), ConclusionNote: "private note", AssigneeID: 88, SubjectRef: "private subject"}
	require.NoError(t, db.Create(&alert).Error)
	for _, actorRole := range []int{1, 10, 100} {
		require.NoError(t, db.Model(&alert).Update("actor_role", actorRole).Error)
		page, err := ListSelfSecurityAlerts(t.Context(), db, SelfSecurityFilter{UserID: 31, Start: now - 60, End: now})
		require.NoError(t, err)
		require.Len(t, page.Items, 1, "owners retain their own account events across known roles")
	}
	result := `{"count_known":true,"affected_count":3,"auth_version":91,"pending_ids":[99]}`
	for _, action := range []model.SecurityAction{
		{AlertID: alert.ID, TargetUserID: 31, ActorID: 88, Action: "clear_api_keys", Outcome: "succeeded", Result: result, Note: "private", ReasonCode: "internal_reason", CreatedAt: now},
		{AlertID: alert.ID, TargetUserID: 32, Action: "disable_account", Outcome: "succeeded", CreatedAt: now},
		{AlertID: alert.ID, TargetUserID: 31, Action: "investigation", Note: "private employee note", Source: "employee", CreatedAt: now},
	} {
		require.NoError(t, db.Create(&action).Error)
	}
	detail, err := GetSelfSecurityAlert(t.Context(), db, 31, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 2)
	require.Equal(t, "192.0.2.31", detail.Events[0].IP)
	require.Equal(t, "auth.session_refresh_reuse", detail.Events[1].Action)
	require.Len(t, detail.Responses, 1)
	require.EqualValues(t, 3, *detail.Responses[0].AffectedCount)
	raw, err := common.Marshal(detail)
	require.NoError(t, err)
	for _, hidden := range []string{"private", "192.0.2.32", "192.0.2.99", "assignee", "actor_id", "actor_role", "subject_ref", "reason_code", "rule_version", "threshold", "facts", "pending_ids", "auth_version", "target_user_id", "conclusion", "version", "note"} {
		require.NotContains(t, string(raw), hidden)
	}
	_, err = GetSelfSecurityAlert(t.Context(), db, 32, alert.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetSelfSecurityAlert(t.Context(), db, 31, alert.ID+1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, db.Model(&alert).Update("purge_state", "deleting").Error)
	_, err = GetSelfSecurityAlert(t.Context(), db, 31, alert.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestSelfSecurityLinkedResponseGroup(t *testing.T) {
	db, canonical := assignmentFixture(t)
	linked := canonical
	linked.ID = 0
	linked.Rule, linked.RuleVersion = "session_reuse", 2
	require.NoError(t, db.Create(&linked).Error)
	group := model.SecurityResponseGroup{TriggerKey: "self-linked-response", TargetUserID: 3, State: "complete"}
	require.NoError(t, db.Create(&group).Error)
	require.NoError(t, db.Create(&[]model.SecurityResponseLink{{AlertID: canonical.ID, GroupID: group.ID}, {AlertID: linked.ID, GroupID: group.ID}}).Error)
	action := model.SecurityAction{AlertID: canonical.ID, ResponseGroupID: &group.ID, TargetUserID: 3, Action: "clear_api_keys", Outcome: "succeeded", Result: `{"count_known":true,"affected_count":3}`, Note: "private investigation"}
	require.NoError(t, db.Create(&action).Error)
	foreign := model.SecurityAction{AlertID: canonical.ID, ResponseGroupID: &group.ID, TargetUserID: 4, Action: "disable_account", Outcome: "succeeded"}
	require.NoError(t, db.Create(&foreign).Error)
	self, err := GetSelfSecurityAlert(t.Context(), db, 3, linked.ID)
	require.NoError(t, err)
	require.Len(t, self.Responses, 1)
	require.Equal(t, action.ID, self.Responses[0].ID)
	require.EqualValues(t, 3, *self.Responses[0].AffectedCount)
	raw, err := common.Marshal(self)
	require.NoError(t, err)
	for _, hidden := range []string{"private investigation", "target_user_id", "response_group_id", "actor_id"} {
		require.NotContains(t, string(raw), hidden)
	}
	_, err = GetSelfSecurityAlert(t.Context(), db, 4, linked.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestSelfSecurityIdentityAndLegacyEvidence(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	for _, tc := range []struct {
		name, rule    string
		role, version int
		visible       bool
	}{
		{"ordinary", "new_login_source", 1, 3, true},
		{"admin", "new_login_source", 10, 3, true},
		{"root", "new_login_source", 100, 3, true},
		{"unknown", "new_login_source", 0, 3, false},
		{"reuse owner", "session_reuse", 0, 2, true},
		{"resource", "sensitive_operation", 1, 2, false},
		{"anonymous failure", "login_failures", 1, 1, false},
		{"future version", "new_login_source", 1, 4, false},
		{"missing history", "new_login_source", 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alert := model.SecurityAlert{ActorID: 31, ActorRole: tc.role, Rule: tc.rule, RuleVersion: tc.version, FirstSeen: now, LastSeen: now}
			require.NoError(t, db.Create(&alert).Error)
			_, err := GetSelfSecurityAlert(t.Context(), db, 31, alert.ID)
			if tc.visible {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			}
		})
	}
	legacy := model.SecurityAlert{ActorID: 31, Rule: "session_reuse", RuleVersion: 1, Occurrences: 1, FirstSeen: now, LastSeen: now}
	require.NoError(t, db.Create(&legacy).Error)
	finding := model.SecurityFindingSnapshot{AlertID: legacy.ID, ActorID: 31, ActorRole: 10, Rule: legacy.Rule}
	require.NoError(t, db.Create(&finding).Error)
	require.NoError(t, db.Model(&legacy).Update("latest_finding_id", finding.ID).Error)
	_, err := GetSelfSecurityAlert(t.Context(), db, 31, legacy.ID)
	require.NoError(t, err)
	allowed, err := customerAlertAccessible(db, legacy, 31)
	require.NoError(t, err)
	require.False(t, allowed, "self access must not broaden employee access to former administrators")
	require.NoError(t, db.Model(&finding).Update("actor_id", 32).Error)
	_, err = GetSelfSecurityAlert(t.Context(), db, 31, legacy.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, db.Model(&finding).Update("actor_id", 31).Error)
	require.NoError(t, db.Model(&legacy).Update("occurrences", 101).Error)
	_, err = GetSelfSecurityAlert(t.Context(), db, 31, legacy.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestSelfSecurityPaginationAndCursorScope(t *testing.T) {
	for _, count := range []int{0, 1, 20, 21, 43} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			db := newWorkerTestDB(t)
			now := time.Now().Unix()
			var expected []int64
			for i := range 42 + count*2 + 22 {
				alert := model.SecurityAlert{ActorID: 31, ActorRole: 1, Rule: "session_reuse", RuleVersion: 1, LastSeen: now - int64(i/3)}
				visible := i >= 42 && i < 42+count*2 && i%2 == 0
				if visible {
					alert.RuleVersion = 2
				}
				require.NoError(t, db.Create(&alert).Error)
				if visible {
					expected = append(expected, alert.ID)
				}
			}
			f := SelfSecurityFilter{UserID: 31, Start: now - 86400, End: now}
			var seen []int64
			for {
				page, err := ListSelfSecurityAlerts(t.Context(), db, f)
				require.NoError(t, err)
				require.Len(t, page.Items, min(20, count-len(seen)))
				for _, alert := range page.Items {
					seen = append(seen, alert.ID)
				}
				if page.NextCursor == "" {
					break
				}
				require.Less(t, len(seen), count)
				f.Cursor = page.NextCursor
				for _, changed := range []SelfSecurityFilter{
					{UserID: 32, Start: f.Start, End: f.End, Cursor: f.Cursor},
					{UserID: 31, Start: f.Start, End: f.End + 1, Cursor: f.Cursor},
				} {
					_, err := ListSelfSecurityAlerts(t.Context(), db, changed)
					require.ErrorIs(t, err, ErrSelfSecurityParams)
				}
			}
			require.ElementsMatch(t, expected, seen)
			for _, invalid := range []SelfSecurityFilter{
				{UserID: 0, Start: now - 1, End: now}, {UserID: 31, Start: 0, End: now},
				{UserID: 31, Start: now - 32*86400, End: now}, {UserID: 31, Start: now, End: now - 1},
				{UserID: 31, Start: now - 1, End: now, Cursor: "invalid"},
				{UserID: 31, Start: now - 1, End: now, Cursor: base64.RawURLEncoding.EncodeToString([]byte(`{"UserID":31,"ID":1}`))},
			} {
				_, err := ListSelfSecurityAlerts(t.Context(), db, invalid)
				require.ErrorIs(t, err, ErrSelfSecurityParams)
			}
		})
	}
}
