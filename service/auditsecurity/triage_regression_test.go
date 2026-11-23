package auditsecurity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSecurityActorUsernames(t *testing.T) {
	db := newWorkerTestDB(t)
	users := []model.User{
		{Id: 31, Username: "alice", AffCode: "actor-alice", Role: 1, Status: 1},
		{Id: 32, Username: "removed", AffCode: "actor-removed", Role: 1, Status: 1},
		{Id: 34, Username: "private-root", AffCode: "actor-root", Role: 100, Status: 1},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Delete(&model.User{}, 32).Error)
	now := time.Now().Unix()
	evidence, err := common.Marshal([]model.SecurityEvent{
		{EventID: "known", ActorID: 31, ActorUsername: "stale-name", At: now, Facts: "{}"},
		{EventID: "anonymous", ActorUsername: "unverified-name", At: now, Facts: "{}"},
	})
	require.NoError(t, err)
	alerts := []model.SecurityAlert{
		{ActorID: 31, ActorUsername: "not-persisted", Visibility: 10, Rule: "sensitive_operation", SubjectRef: "actor:31", Status: "open", LastSeen: now, Evidence: string(evidence)},
		{ActorID: 0, Visibility: 10, Rule: "login_failures", SubjectRef: model.SecurityAccountRef("alice"), Status: "open", LastSeen: now, Evidence: "[]"},
		{ActorID: 32, Visibility: 10, Rule: "sensitive_operation", Status: "open", LastSeen: now, Evidence: "[]"},
		{ActorID: 99, Visibility: 10, Rule: "sensitive_operation", Status: "open", LastSeen: now, Evidence: "[]"},
		{ActorID: 34, Visibility: 100, Rule: "sensitive_operation", SubjectRef: "actor:31", Status: "open", LastSeen: now, Evidence: "[]"},
	}
	require.NoError(t, db.Create(&alerts).Error)
	findings := []model.SecurityFindingSnapshot{
		{AlertID: alerts[0].ID, ActorID: 31, SubjectRef: "actor:31", TriggerEventID: "known", TriggerAt: now},
		{AlertID: alerts[4].ID, ActorID: 34, SubjectRef: "actor:31", TriggerEventID: "hidden", TriggerAt: now},
	}
	require.NoError(t, db.Create(&findings).Error)
	require.NoError(t, db.Create(&[]model.SecurityAction{
		{AlertID: alerts[0].ID, ActorID: 31, ActorRole: 1, Action: "investigation"},
		{AlertID: alerts[0].ID, ActorID: 34, ActorRole: 100, Action: "investigation"},
	}).Error)
	filter := AlertFilter{Start: now - 10, End: now + 10, Limit: 20}
	page, err := ListAlerts(t.Context(), db, 10, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 4)
	for _, alert := range page.Items {
		if alert.ActorID == 31 {
			require.Equal(t, "alice", alert.ActorUsername)
		} else {
			require.Empty(t, alert.ActorUsername)
		}
	}
	detail, err := GetAlert(t.Context(), db, 10, alerts[0].ID)
	require.NoError(t, err)
	require.Equal(t, "alice", detail.Alert.ActorUsername)
	require.Len(t, detail.Events, 2)
	for _, event := range detail.Events {
		if event.ActorID == 31 {
			require.Equal(t, "alice", event.ActorUsername)
		} else {
			require.Empty(t, event.ActorUsername)
		}
	}
	require.Equal(t, "alice", detail.Findings[0].ActorUsername)
	require.Len(t, detail.Actions, 1)
	require.Equal(t, "alice", detail.Actions[0].ActorUsername)
	_, err = GetAlert(t.Context(), db, 10, alerts[4].ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	rootDetail, err := GetAlert(t.Context(), db, 100, alerts[4].ID)
	require.NoError(t, err)
	require.Equal(t, "private-root", rootDetail.Alert.ActorUsername)
	related, err := GetAlertRelations(t.Context(), db, 10, 31, alerts[0].ID, "account", "", 20, int(now-10), int(now+10))
	require.NoError(t, err)
	require.Len(t, related.Items, 1)
	require.Equal(t, "alice", related.Items[0].ActorUsername)
	export, err := ExportAlerts(t.Context(), db, 10, filter, 20)
	require.NoError(t, err)
	require.Len(t, export.Items, 4)
	for _, item := range export.Items {
		if item.Alert.ActorID == 31 {
			require.Equal(t, "alice", item.Alert.ActorUsername)
		}
	}
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 31).Update("username", "renamed").Error)
	detail, err = GetAlert(t.Context(), db, 10, alerts[0].ID)
	require.NoError(t, err)
	require.Equal(t, "renamed", detail.Alert.ActorUsername)
	var persisted model.SecurityAlert
	require.NoError(t, db.First(&persisted, alerts[0].ID).Error)
	require.Empty(t, persisted.ActorUsername)
	require.Equal(t, string(evidence), persisted.Evidence)
	for _, record := range []any{&model.SecurityAlert{}, &model.SecurityEvent{}, &model.SecurityAction{}, &model.SecurityFindingSnapshot{}} {
		require.False(t, db.Migrator().HasColumn(record, "actor_username"))
	}
}

func TestSecurityActorNamesBatchingAndFailure(t *testing.T) {
	db := newWorkerTestDB(t)
	users := make([]model.User, 205)
	for i := range users {
		users[i] = model.User{Id: i + 1, Username: fmt.Sprintf("actor-%03d", i), AffCode: fmt.Sprintf("actor-%03d", i)}
	}
	require.NoError(t, db.CreateInBatches(users, 100).Error)
	names := make([]string, 410)
	actors := make([]securityActorName, len(names))
	for i := range actors {
		actors[i] = securityActorName{i%205 + 1, &names[i]}
	}
	var batches []int
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:actor-name-batches", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" && !tx.DryRun {
			batches = append(batches, len(tx.Statement.Vars))
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:actor-name-batches") })
	require.NoError(t, loadSecurityActorNames(t.Context(), db, actors))
	require.Equal(t, []int{100, 100, 5}, batches)
	for i, name := range names {
		require.Equal(t, users[i%205].Username, name)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, loadSecurityActorNames(ctx, db, actors[:1]))
	require.Empty(t, names[0])
	var unknown = "must-clear"
	require.NoError(t, loadSecurityActorNames(t.Context(), db, []securityActorName{{0, &unknown}}))
	require.Empty(t, unknown)
}

func TestPublicSecurityFactsExcludeExecutionProvenance(t *testing.T) {
	facts := Facts{
		Identity: &model.AuditIdentity{}, LivePolicyAt: 100, FirstObservedAt: 101,
		NewManagementSource: true, PermissionIncrease: true, TokenID: 7,
		SourceBaseline: &sourceBaselineEvidence{SourceFirstSeen: 100, BaselineFirstSeen: 1, BaselineLastSeen: 99},
	}
	raw, err := common.Marshal(facts)
	require.NoError(t, err)
	public := publicSecurityEvent(model.SecurityEvent{Facts: string(raw)})
	var values map[string]any
	require.NoError(t, common.UnmarshalJsonStr(public.Facts, &values))
	for _, key := range []string{"identity", "live_policy_at", "first_observed_at", "new_management_source", "source_baseline"} {
		require.NotContains(t, values, key)
	}
	require.Equal(t, true, values["permission_increase"])
	require.EqualValues(t, 7, values["token_id"])
}

func TestTriageEvidenceVersionsAndFindingSnapshots(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "triage-evidence")
	require.NoError(t, err)
	now := time.Now().Unix()
	legacy := model.SecurityAlert{Rule: "login_after_failures", RuleVersion: 1, SubjectRef: "account:review", Visibility: 100, ResourceType: "account", Status: "open", Evidence: `[{"event_id":"legacy-success","action":"login"}]`}
	require.NoError(t, db.Create(&legacy).Error)
	events := make([]model.SecurityEvent, 0, 6)
	for i := range 5 {
		events = append(events, model.SecurityEvent{EventID: fmt.Sprintf("failure-%d", i), At: now - 60 + int64(i), Action: "auth.login_failed", Reason: "invalid_credentials", SubjectRef: "account:review", Visibility: 100, ResourceType: "account", IP: "203.0.113.9", Facts: "{}"})
	}
	events = append(events, model.SecurityEvent{EventID: "success", At: now, Action: "login", Success: true, SubjectRef: "account:review", Visibility: 100, ResourceType: "account", ActorID: 17, ActorRole: 1, IP: "203.0.113.9", Facts: "{}"})
	next := state
	next.CursorAt, next.CursorEvent = now, "success"
	require.NoError(t, Apply(ctx, db, state, events, next, operation_setting.DefaultAuditSecuritySetting()))
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ? AND rule_version = ?", "login_after_failures", 3).Take(&alert).Error)
	require.NotEqual(t, legacy.ID, alert.ID)
	detail, err := GetAlert(ctx, db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 6)
	require.Len(t, detail.Findings, 1)
	require.Equal(t, 5, detail.Findings[0].Threshold)
	require.Equal(t, 900, detail.Findings[0].WindowSeconds)
	require.Equal(t, 5, detail.Findings[0].Dimensions["failures"])
	failures := 0
	for _, event := range detail.Events {
		if event.Action == "auth.login_failed" {
			failures++
		}
	}
	require.Equal(t, 5, failures)
	require.NotZero(t, alert.UpdatedAt)
}

func TestTriageResourceAndActivityContracts(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "triage-resource")
	require.NoError(t, err)
	now := time.Now().Unix()
	events := []model.SecurityEvent{
		{EventID: "delete-channel", At: now - 10, ActorID: 7, ActorRole: 100, Visibility: 100, SubjectRef: "actor:7", ResourceType: "channel", Action: "channel.delete", Success: true, Count: 1, Facts: "{}"},
		{EventID: "delete-user", At: now, ActorID: 7, ActorRole: 100, Visibility: 100, SubjectRef: "actor:7", ResourceType: "user", Action: "user.delete", Success: true, Count: 1, Facts: "{}"},
	}
	next := state
	next.CursorAt, next.CursorEvent = now, "delete-user"
	require.NoError(t, Apply(ctx, db, state, events, next, operation_setting.DefaultAuditSecuritySetting()))
	for _, resource := range []string{"channel", "user"} {
		page, err := ListAlerts(ctx, db, 100, AlertFilter{Start: now - 120, End: now + 1, Resource: resource, Limit: 50})
		require.NoError(t, err)
		require.Len(t, page.Items, 1, resource)
		require.Equal(t, resource, page.Items[0].ResourceType)
	}
	first, err := ListAlerts(ctx, db, 100, AlertFilter{View: "activity", Start: now - 120, End: now + 1, Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.IsType(t, "", first.Next)
	require.NotEmpty(t, first.Next)
	second, err := ListAlerts(ctx, db, 100, AlertFilter{View: "activity", Start: now - 120, End: now + 1, Limit: 1, Cursor: first.Next.(string)})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.NotEqual(t, first.Items[0].ID, second.Items[0].ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return model.AdvanceAuditActivityRevision(tx) }))
	_, err = ListAlerts(ctx, db, 100, AlertFilter{View: "activity", Start: now - 120, End: now + 1, Limit: 1, Cursor: first.Next.(string)})
	require.ErrorIs(t, err, ErrActivityCursor)
}

func TestCleanupPurgingCannotReopenOldAlert(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := context.Background()
	now := time.Now().Unix()
	closedAt := now - 1000
	old := model.SecurityAlert{Rule: "sensitive_operation", RuleVersion: 2, SubjectRef: "actor:77", Visibility: 100, ActorID: 77, ResourceType: "channel", Status: "resolved", ClosedAt: &closedAt, LastSeen: now - 900}
	require.NoError(t, db.Create(&old).Error)
	drained, err := model.CleanupAuditSecurityAlerts(ctx, db, now-100)
	require.NoError(t, err)
	require.False(t, drained)
	require.NoError(t, db.First(&old, old.ID).Error)
	require.Equal(t, "deleting", old.PurgeState)
	state, err := model.ClaimAuditSecurity(ctx, db, "purge-reopen")
	require.NoError(t, err)
	event := model.SecurityEvent{EventID: "new-hit-during-purge", At: now, ActorID: 77, ActorRole: 100, Visibility: 100, SubjectRef: "actor:77", ResourceType: "channel", Action: "channel.delete", Success: true, Count: 1, Facts: "{}"}
	next := state
	next.CursorAt, next.CursorEvent = now, event.EventID
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, next, operation_setting.DefaultAuditSecuritySetting()))
	var replacement model.SecurityAlert
	require.NoError(t, db.Where("subject_ref = ? AND purge_state = ?", "actor:77", "").Take(&replacement).Error)
	require.NotEqual(t, old.ID, replacement.ID)
	for range 5 {
		_, err = model.CleanupAuditSecurityAlerts(ctx, db, now-100)
		require.NoError(t, err)
	}
	require.ErrorIs(t, db.First(&model.SecurityAlert{}, old.ID).Error, gorm.ErrRecordNotFound)
	require.NoError(t, db.First(&replacement, replacement.ID).Error)
	detail, err := GetAlert(ctx, db, 100, replacement.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	require.Len(t, detail.Findings, 1)
}

func TestIPIntelligenceQualityAndReservedAddresses(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.SecurityGeoBaseline{AccountID: 17, Latitude: 40.71, Longitude: -74.0, LastAt: now - 3600, LastEventID: "previous"}).Error)
	lat, lon := 51.5, -0.12
	event := model.SecurityEvent{EventID: "london", At: now, ActorID: 17, Action: "login", Success: true, SubjectRef: "account:17", IP: "8.8.8.8"}
	var facts Facts
	facts.IPIntelligence = &IPIntelligenceSnapshot{Accuracy: "", Latitude: &lat, Longitude: &lon}
	raw, _ := common.Marshal(facts)
	event.Facts = string(raw)
	findings, err := evaluateIPIntelligenceFindings(db, event, operation_setting.DefaultAuditSecuritySetting())
	require.NoError(t, err)
	require.Empty(t, findings, "unknown accuracy must not create a travel conclusion")
	facts.IPIntelligence.Accuracy = "HIGH"
	raw, _ = common.Marshal(facts)
	event.Facts = string(raw)
	findings, err = evaluateIPIntelligenceFindings(db, event, operation_setting.DefaultAuditSecuritySetting())
	require.NoError(t, err)
	require.Len(t, findings, 1)
	require.Equal(t, "impossible_travel", findings[0].Rule)
	for _, ip := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1", "127.0.0.1", "10.0.0.1", "224.0.0.1"} {
		require.False(t, publicIPForIntelligence(ip), ip)
	}
	require.True(t, publicIPForIntelligence("8.8.8.8"))
}
