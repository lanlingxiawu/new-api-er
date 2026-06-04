package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelAvailabilityControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.GroupStatus{}, &model.GroupModelStatus{}))
	return db
}

func TestBuildModelsWithAvailabilityDefaultsEnabledModelsToAvailable(t *testing.T) {
	db := setupModelAvailabilityControllerTestDB(t)

	result := buildModelsWithAvailability([]string{"gpt-4o", "claude-3"}, "default")

	require.Len(t, result.Models, 2)
	require.True(t, result.Models[0].Available)
	require.Empty(t, result.Models[0].Reason)
	require.True(t, result.Models[1].Available)
	require.Empty(t, result.Models[1].Reason)

	require.NoError(t, db.Create(&model.GroupStatus{
		UserGroup:    "default",
		LastTestTime: 123,
	}).Error)
	require.NoError(t, model.UpsertGroupModelStatus(&model.GroupModelStatus{
		UserGroup: "default", ModelName: "gpt-4o", Available: false, LastTestTime: 123,
	}))
	require.NoError(t, model.UpsertGroupModelStatus(&model.GroupModelStatus{
		UserGroup: "default", ModelName: "claude-3", Available: true, LastTestTime: 123,
	}))

	result = buildModelsWithAvailability([]string{"gpt-4o", "claude-3"}, "default")
	byName := make(map[string]modelAvailabilityForTest)
	for _, item := range result.Models {
		byName[item.Name] = modelAvailabilityForTest{
			Available:       item.Available,
			Reason:          item.Reason,
			LastCheckedTime: item.LastCheckedTime,
		}
	}

	require.False(t, byName["gpt-4o"].Available)
	require.Equal(t, "Repairing", byName["gpt-4o"].Reason)
	require.Equal(t, int64(123), byName["gpt-4o"].LastCheckedTime)
	require.True(t, byName["claude-3"].Available)
	require.Empty(t, byName["claude-3"].Reason)
	require.Equal(t, int64(123), byName["claude-3"].LastCheckedTime)
}

func TestBuildGroupStatusInfoTreatsMissingModelStatusAsAvailable(t *testing.T) {
	db := setupModelAvailabilityControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "gpt-4o", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "claude-3", ChannelId: 2, Enabled: true},
	}).Error)

	info, ok := buildGroupStatusInfo("default")
	require.True(t, ok)
	require.Equal(t, 2, info.AvailableModels)
	require.Equal(t, 2, info.TotalModels)
	require.Equal(t, 100.0, info.AvailabilityRate)

	require.NoError(t, model.UpsertGroupModelStatus(&model.GroupModelStatus{
		UserGroup: "default",
		ModelName: "claude-3",
		Available: false,
	}))

	info, ok = buildGroupStatusInfo("default")
	require.True(t, ok)
	require.Equal(t, 1, info.AvailableModels)
	require.Equal(t, 2, info.TotalModels)
	require.Equal(t, 50.0, info.AvailabilityRate)
}

type modelAvailabilityForTest struct {
	Available       bool
	Reason          string
	LastCheckedTime int64
}
