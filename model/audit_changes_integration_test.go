package model

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func auditChannelFixture(t *testing.T) Channel {
	t.Helper()
	if DB == nil {
		t.Skip("real database not configured")
	}
	priority := int64(9)
	rows := []Channel{{Name: fmt.Sprintf("audit-%d", time.Now().UnixNano()), Key: "audit-old-credential", Models: "audit-model", Group: "default", Priority: &priority, DailyQuotaLimit: 100}}
	require.NoError(t, BatchInsertChannels(rows, 41))
	id := rows[0].Id
	t.Cleanup(func() {
		DB.Where("channel_id = ?", id).Delete(&ChannelCostConfig{})
		DB.Where("channel_id = ?", id).Delete(&ChannelCreator{})
		DB.Where("channel_id = ?", id).Delete(&Ability{})
		DB.Where("id = ?", id).Delete(&Channel{})
	})
	return rows[0]
}

func TestAuditChannelCommittedWriteSetAndRollback(t *testing.T) {
	channel := auditChannelFixture(t)
	require.NoError(t, UpsertChannelCostConfig(channel.Id, 1.5, ""))
	zero := int64(0)
	zeroCost := 0.0
	change := Channel{Id: channel.Id, Name: "audit-after", Key: "audit-new-credential", Priority: &zero, CostRatio: &zeroCost}
	audit := NewAuditChanges()
	require.NoError(t, change.UpdateConfigurationWithAudit(DailyLimitEdit{QuotaLimit: &zero}, audit, 41))
	require.Len(t, audit.Targets, 2)
	require.Equal(t, "atomic", audit.Targets[0].Consistency)
	require.Equal(t, "observed", audit.Targets[1].Consistency)
	fields := map[string]AuditFieldChange{}
	for _, target := range audit.Targets {
		for _, field := range target.Fields {
			fields[field.Path[0]] = field
		}
	}
	require.Equal(t, `9`, string(fields["priority"].Old.Value))
	require.Equal(t, `0`, string(fields["priority"].New.Value))
	require.Equal(t, `100`, string(fields["daily_quota_limit"].Old.Value))
	require.Equal(t, `0`, string(fields["daily_quota_limit"].New.Value))
	require.Equal(t, `1.5`, string(fields["cost_ratio"].Old.Value))
	require.Equal(t, `0`, string(fields["cost_ratio"].New.Value))
	require.NotContains(t, fields, "models", "omitted models were not written")
	encoded, err := common.Marshal(audit)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "credential")
	callback := "audit_test_rollback_abilities"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "abilities" {
			tx.AddError(errors.New("audit test rollback"))
		}
	}))
	t.Cleanup(func() { DB.Callback().Create().Remove(callback) })
	failed := NewAuditChanges()
	change.Name = "must-roll-back"
	require.Error(t, change.UpdateConfigurationWithAudit(DailyLimitEdit{}, failed, 41))
	require.Empty(t, failed.Targets)
	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	require.Equal(t, "audit-after", stored.Name)
}

func TestAuditChannelTagAndDailyLimitUseNormalizedWrites(t *testing.T) {
	channel := auditChannelFixture(t)
	tag := channel.Name
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{"tag": tag, "daily_limit_auto_recover": 1, "daily_limit_recover_minutes": 30}).Error)
	audit := NewAuditChanges()
	zero := 0
	_, err := UpdateChannelDailyLimitByIds([]int{channel.Id}, DailyLimitEdit{AutoRecover: &zero}, audit)
	require.NoError(t, err)
	require.Len(t, audit.Targets, 1)
	fields := map[string]AuditFieldChange{}
	for _, field := range audit.Targets[0].Fields {
		fields[field.Path[0]] = field
	}
	require.Equal(t, `30`, string(fields["daily_limit_recover_minutes"].Old.Value))
	require.Equal(t, `0`, string(fields["daily_limit_recover_minutes"].New.Value))
	newTag := tag + "-renamed"
	priority := int64(0)
	require.NoError(t, EditChannelByTag(tag, &newTag, nil, nil, nil, &priority, nil, nil, nil, audit))
	require.Len(t, audit.Targets, 2)
	require.Equal(t, strconv.Itoa(channel.Id), audit.Targets[1].ID)
}

func TestAuditOptionReceiptUsesStoredValuesAndRollsBack(t *testing.T) {
	if DB == nil {
		t.Skip("real database not configured")
	}
	prefix := fmt.Sprintf("audit_option_%d", time.Now().UnixNano())
	keys := []string{prefix + "_first", prefix + "_second"}
	for _, key := range keys {
		require.NoError(t, DB.Create(&Option{Key: key, Value: "old-secret"}).Error)
	}
	t.Cleanup(func() { DB.Where(commonKeyCol+" IN ?", keys).Delete(&Option{}) })
	audit := NewAuditChanges()
	require.NoError(t, persistOptionsTx(map[string]string{keys[0]: "new-secret", keys[1]: "new-secret"}, audit))
	require.Len(t, audit.Targets, 2)
	encoded, err := common.Marshal(audit)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "old-secret")
	require.NotContains(t, string(encoded), "new-secret")
	for _, target := range audit.Targets {
		require.Equal(t, "redacted", target.Fields[0].Old.State)
	}
	callback := "audit_test_options_rollback"
	writes := 0
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if option, ok := tx.Statement.Dest.(*Option); ok && (option.Key == keys[0] || option.Key == keys[1]) {
			writes++
			if writes == 2 {
				tx.AddError(errors.New("audit test options rollback"))
			}
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(callback) })
	failed := NewAuditChanges()
	require.Error(t, persistOptionsTx(map[string]string{keys[0]: "failed", keys[1]: "failed"}, failed))
	require.Empty(t, failed.Targets)
	for _, key := range keys {
		var row Option
		require.NoError(t, DB.Where(&Option{Key: key}).First(&row).Error)
		require.Equal(t, "new-secret", row.Value)
	}
}

func TestAuditChangesDatabaseRoundTripAndSelfProjection(t *testing.T) {
	if DB == nil || LOG_DB == nil {
		t.Skip("real databases not configured")
	}
	id := int(time.Now().UnixNano()%1000000000) + 1000000000
	event := fmt.Sprintf("audit-values-%d", time.Now().UnixNano())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("PUT", "/audit-test", nil)
	c.Set("id", id)
	c.Set("role", common.RoleAdminUser)
	RequestAuditChanges(c).Add(AuditTarget("channel", 42, "before-name"), AuditFields{"priority": int64(9007199254740993), "key": "hidden-before"}, AuditFields{"priority": 0, "key": "hidden-after"})
	RecordAuditLog(c, AuditLog{EventId: event, UserId: id, Username: "audit-test", ActorRole: common.RoleAdminUser, Category: AuditCategoryOperation, Action: "channel.update", Success: true})
	t.Cleanup(func() { LOG_DB.Where("event_id = ?", event).Delete(&AuditLog{}) })
	var stored AuditLog
	require.NoError(t, LOG_DB.Where("event_id = ?", event).First(&stored).Error)
	require.NotNil(t, stored.Other.Changes)
	encoded, err := common.Marshal(stored.Other)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"9007199254740993"`)
	require.NotContains(t, string(encoded), "hidden-before")
	ProjectAuditLogs([]*AuditLog{&stored}, true, common.RoleRootUser)
	require.Nil(t, stored.Other.Changes)
}

func TestAuditUserSettingsCapturePreservedAndSecretFields(t *testing.T) {
	if DB == nil {
		t.Skip("real database not configured")
	}
	user := User{Username: fmt.Sprintf("audit-user-%d", time.Now().UnixNano()), Password: "test", Setting: `{"stream_protocol_validation":false,"language":"en","webhook_secret":"old-private"}`}
	require.NoError(t, DB.Create(&user).Error)
	t.Cleanup(func() { DB.Unscoped().Delete(&User{}, user.Id) })
	audit := NewAuditChanges()
	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh", WebhookSecret: "new-private"}, audit))
	encoded, err := common.Marshal(audit)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
	require.Contains(t, string(encoded), `"language"`)
	require.NotContains(t, string(encoded), `"path":["setting","stream_protocol_validation"]`)
	require.Equal(t, "self", audit.Targets[0].Visibility)
}
