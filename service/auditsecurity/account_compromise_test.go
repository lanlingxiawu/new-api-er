package auditsecurity

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func compromiseEvent(id string, actor int, at int64, ip, action string) model.SecurityEvent {
	return Normalize(model.AuditLog{EventId: id, RequestId: id, UserId: actor, ActorRole: common.RoleCommonUser, CreatedAt: at, Ip: ip, UserAgent: "fixture-client", Action: action, Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Identity: &model.AuditIdentity{ActorID: actor, Role: common.RoleCommonUser, AuthVersion: 1}, Params: model.AuditFields{"id": actor, "returned_ids": []int{actor}, "count": 1}}}})
}

func TestMultiAccountKeyAccessRequiresEachTargetsOwnEvidence(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	var events []model.SecurityEvent
	for i := range 7 {
		events = append(events, compromiseEvent(fmt.Sprint("login", i), i+1, 1000+int64(i*5), "8.8.8.8", "login"))
	}
	require.Empty(t, EvaluateAccountCompromise(events[6], events[:6], cfg, false))
	for i := range 7 {
		event := compromiseEvent(fmt.Sprint("key", i), i+1, 1050+int64(i), "8.8.8.8", "token.key_view_batch")
		findings := EvaluateAccountCompromise(event, events, cfg, false)
		if i < 2 {
			require.Empty(t, findings)
		} else {
			require.Len(t, findings, i+1)
			for _, f := range findings {
				require.Equal(t, "multi_account_key_access", f.Rule)
				require.NotNil(t, f.TargetEvent)
				require.Equal(t, "token.key_view_batch", f.TargetEvent.Action)
				require.Equal(t, fmt.Sprintf("actor:%d", f.TargetEvent.ActorID), f.SubjectRef)
			}
		}
		events = append(events, event)
	}
	for i := range events {
		events[i].Success = false
	}
	require.Empty(t, EvaluateAccountCompromise(events[len(events)-1], events, cfg, false))
}

func TestMultiSourceKeyAccessRejectsWeakAndIncompleteEvidence(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	var events []model.SecurityEvent
	for i := range 5 {
		events = append(events, compromiseEvent(fmt.Sprint("login", i), 7, 1000+int64(i), fmt.Sprintf("8.8.8.%d", i+1), "login"))
	}
	for i := range 3 {
		events = append(events, compromiseEvent(fmt.Sprint("key", i), 7, 1010+int64(i), fmt.Sprintf("8.8.8.%d", i+1), "token.key_view"))
	}
	current := events[len(events)-1]
	findings := EvaluateAccountCompromise(current, events, cfg, false)
	require.Len(t, findings, 1)
	require.Equal(t, "multi_source_key_access", findings[0].Rule)
	// An unrelated later key read cannot refresh the age of an older match.
	otherIP := compromiseEvent("unrelated-key", 7, 1100, "1.1.1.1", "token.key_view")
	require.Empty(t, EvaluateAccountCompromise(otherIP, events, cfg, false))
	for _, change := range []func([]model.SecurityEvent){
		func(rows []model.SecurityEvent) {
			for i := range rows {
				rows[i].IP = "127.0.0.1"
			}
		},
		func(rows []model.SecurityEvent) {
			for i := range 5 {
				rows[i].At = 2000
			}
		},
		func(rows []model.SecurityEvent) { rows[5].Facts = `{"evidence_incomplete":true}` },
		func(rows []model.SecurityEvent) { rows[5].RequestID = rows[6].RequestID },
		func(rows []model.SecurityEvent) { rows[5].Facts = `{"id":0}` },
	} {
		rows := append([]model.SecurityEvent(nil), events...)
		change(rows)
		require.Empty(t, EvaluateAccountCompromise(rows[len(rows)-1], rows, cfg, false))
	}
}

func TestNewManagementSourceRequiresActualDistinctChannelChanges(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	var events []model.SecurityEvent
	for i := range 10 {
		e := compromiseEvent(fmt.Sprint(i), 7, 1000+int64(i), "8.8.8.8", "channel.update")
		e.ActorRole, e.TargetID, e.Facts = common.RoleAdminUser, i+1, `{"changed_fields":["base_url"],"changed":true}`
		events = append(events, e)
	}
	require.Empty(t, EvaluateAccountCompromise(events[9], events, cfg, false))
	require.Empty(t, EvaluateAccountCompromise(events[8], events[:9], cfg, true))
	require.Len(t, EvaluateAccountCompromise(events[9], events, cfg, true), 1)
	events[0].Facts = `{"submitted_fields":["base_url"]}`
	require.Empty(t, EvaluateAccountCompromise(events[9], events, cfg, true))
}
