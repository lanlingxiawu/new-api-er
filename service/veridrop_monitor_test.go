package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestInferVeridropProtocol(t *testing.T) {
	tests := []struct {
		name    string
		channel *model.Channel
		want    string
	}{
		{
			name:    "anthropic channel",
			channel: &model.Channel{Type: constant.ChannelTypeAnthropic},
			want:    "anthropic",
		},
		{
			name:    "gemini channel",
			channel: &model.Channel{Type: constant.ChannelTypeGemini},
			want:    "gemini",
		},
		{
			name:    "openai compatible channel",
			channel: &model.Channel{Type: constant.ChannelTypeOpenRouter},
			want:    "openai",
		},
		{
			name: "advanced custom single protocol",
			channel: func() *model.Channel {
				channel := &model.Channel{Type: constant.ChannelTypeAdvancedCustom}
				channel.SetOtherSettings(dto.ChannelOtherSettings{
					AdvancedCustom: &dto.AdvancedCustomConfig{
						Routes: []dto.AdvancedCustomRoute{
							{
								IncomingPath: "/v1/chat/completions",
								Converter:    "openai_chat_completions_to_anthropic_messages",
							},
						},
					},
				})
				return channel
			}(),
			want: "anthropic",
		},
		{
			name: "advanced custom mixed protocols is ambiguous",
			channel: func() *model.Channel {
				channel := &model.Channel{Type: constant.ChannelTypeAdvancedCustom}
				channel.SetOtherSettings(dto.ChannelOtherSettings{
					AdvancedCustom: &dto.AdvancedCustomConfig{
						Routes: []dto.AdvancedCustomRoute{
							{IncomingPath: "/v1/messages"},
							{IncomingPath: "/v1beta/models/gemini:generateContent"},
						},
					},
				})
				return channel
			}(),
			want: "",
		},
		{
			name:    "unknown channel",
			channel: &model.Channel{Type: constant.ChannelTypeMidjourney},
			want:    "",
		},
		{
			name:    "nil channel",
			channel: nil,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, inferVeridropProtocol(tt.channel))
		})
	}
}

func TestApplyVeridropPayloadDefaultsPreservesExplicitFalse(t *testing.T) {
	payload := applyVeridropPayloadDefaults(VeridropDetectionTaskPayload{
		IncludeLongContext:        common.GetPointer(false),
		IncludeLongContextExtreme: common.GetPointer(false),
	}, veridropSettingsSnapshot{
		IncludeLongContext:        true,
		IncludeLongContextExtreme: true,
	})

	require.NotNil(t, payload.IncludeLongContext)
	require.NotNil(t, payload.IncludeLongContextExtreme)
	require.False(t, *payload.IncludeLongContext)
	require.False(t, *payload.IncludeLongContextExtreme)
}

func TestRunVeridropDetectionJobsCancelsUndispatchedRecords(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelVeridropDetection{}))
	first := &model.ChannelVeridropDetection{Status: model.ChannelVeridropDetectionQueued}
	second := &model.ChannelVeridropDetection{Status: model.ChannelVeridropDetectionQueued}
	require.NoError(t, model.CreateChannelVeridropDetection(first))
	require.NoError(t, model.CreateChannelVeridropDetection(second))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id IN ?", []int64{first.ID, second.ID}).Delete(&model.ChannelVeridropDetection{}).Error)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, processed := runVeridropDetectionJobs(ctx, []veridropDetectionJob{
		{Detection: first},
		{Detection: second},
	}, VeridropDetectionTaskPayload{}, veridropSettingsSnapshot{MaxConcurrent: 1}, nil, 0, 2)

	require.Equal(t, 2, processed)
	require.Equal(t, 2, summary.Cancelled)
	for _, id := range []int64{first.ID, second.ID} {
		detection, err := model.GetChannelVeridropDetectionByID(id)
		require.NoError(t, err)
		require.Equal(t, model.ChannelVeridropDetectionCancelled, detection.Status)
	}
}

func TestNormalizeVeridropModelNames(t *testing.T) {
	require.Equal(
		t,
		[]string{"claude-haiku-4-5", "gpt-5"},
		normalizeVeridropModelNames([]string{" claude-haiku-4-5 ", "", "gpt-5", "gpt-5"}),
	)
}

func TestRunVeridropDetectionTaskSingleChannelExpandsAllModels(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.ChannelVeridropDetection{}))
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })

	var submitted struct {
		sync.Mutex
		models []string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/detect/openai":
			require.NoError(t, r.ParseForm())
			modelName := r.Form.Get("model")
			submitted.Lock()
			submitted.models = append(submitted.models, modelName)
			submitted.Unlock()
			_, _ = w.Write([]byte(`{"job_id":"job-` + modelName + `"}`))
		case strings.HasPrefix(r.URL.Path, "/api/status/job-"):
			_, _ = w.Write([]byte(`{"status":"done"}`))
		case strings.HasPrefix(r.URL.Path, "/api/result/job-"):
			_, _ = w.Write([]byte(`{"protocol":"openai","total_score":90,"verdict":"passed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setting.Enabled = true
	setting.BaseURL = server.URL
	setting.MaxConcurrent = 2
	setting.SubmitTimeoutSeconds = 2
	setting.PollIntervalSeconds = 1
	setting.JobTimeoutSeconds = 2
	operation_setting.ReplaceVeridropMonitorSetting(setting)

	channelID := 887000000 + int(time.Now().UnixNano()%1000000)
	channel := &model.Channel{
		Id:        channelID,
		Name:      "veridrop-single-all-models",
		Type:      constant.ChannelTypeOpenAI,
		Key:       "sk-single-all-models",
		Status:    common.ChannelStatusEnabled,
		BaseURL:   common.GetPointer("https://single.example/v1"),
		Models:    " gpt-a, gpt-b, gpt-a, ,gpt-c ",
		TestModel: common.GetPointer("gpt-test-only"),
		Group:     "default",
	}
	require.NoError(t, model.DB.Create(channel).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id = ?", channelID).Delete(&model.ChannelVeridropDetection{}).Error)
		require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
	})

	progress := make([][2]int, 0, 3)
	summary := RunVeridropDetectionTask(context.Background(), VeridropDetectionTaskPayload{
		ChannelID: channelID,
	}, func(processed, total int) {
		progress = append(progress, [2]int{processed, total})
	})

	require.Equal(t, 1, summary.Channels)
	require.Equal(t, 3, summary.Models)
	require.Equal(t, 3, summary.Created)
	require.Equal(t, 3, summary.Succeeded)
	require.Equal(t, [2]int{3, 3}, progress[len(progress)-1])
	submitted.Lock()
	require.ElementsMatch(t, []string{"gpt-a", "gpt-b", "gpt-c"}, submitted.models)
	submitted.models = nil
	submitted.Unlock()

	overrideSummary := RunVeridropDetectionTask(context.Background(), VeridropDetectionTaskPayload{
		ChannelID: channelID,
		Model:     " gpt-override ",
	}, nil)
	require.Equal(t, 1, overrideSummary.Channels)
	require.Equal(t, 1, overrideSummary.Models)
	require.Equal(t, 1, overrideSummary.Created)
	require.Equal(t, 1, overrideSummary.Succeeded)
	submitted.Lock()
	require.Equal(t, []string{"gpt-override"}, submitted.models)
	submitted.models = nil
	submitted.Unlock()

	blankOverrideSummary := RunVeridropDetectionTask(context.Background(), VeridropDetectionTaskPayload{
		ChannelID: channelID,
		Model:     "   ",
	}, nil)
	require.Equal(t, 1, blankOverrideSummary.Channels)
	require.Equal(t, 3, blankOverrideSummary.Models)
	require.Equal(t, 3, blankOverrideSummary.Created)
	require.Equal(t, 3, blankOverrideSummary.Succeeded)
	submitted.Lock()
	require.ElementsMatch(t, []string{"gpt-a", "gpt-b", "gpt-c"}, submitted.models)
	submitted.Unlock()

	var rows []*model.ChannelVeridropDetection
	require.NoError(t, model.DB.Where("channel_id = ?", channelID).Find(&rows).Error)
	require.Len(t, rows, 7)
	for _, row := range rows {
		require.Empty(t, row.BatchTaskID)
	}
}

func TestRunVeridropDetectionTaskSingleChannelUnsupportedProtocolCompletesProgress(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.ChannelVeridropDetection{}))
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })
	setting.Enabled = true
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)

	channelID := 889000000 + int(time.Now().UnixNano()%1000000)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Name:    "veridrop-single-unsupported",
		Type:    constant.ChannelTypeMidjourney,
		Key:     "sk-single-unsupported",
		Status:  common.ChannelStatusEnabled,
		BaseURL: common.GetPointer("https://single.example/v1"),
		Models:  "mj-a,mj-b",
		Group:   "default",
	}).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id = ?", channelID).Delete(&model.ChannelVeridropDetection{}).Error)
		require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
	})

	var progress [2]int
	summary := RunVeridropDetectionTask(context.Background(), VeridropDetectionTaskPayload{
		ChannelID: channelID,
	}, func(processed, total int) {
		progress = [2]int{processed, total}
	})

	require.Equal(t, 1, summary.Channels)
	require.Zero(t, summary.Models)
	require.Zero(t, summary.Created)
	require.Equal(t, 2, summary.Skipped)
	require.Equal(t, [2]int{2, 2}, progress)

	var rows []*model.ChannelVeridropDetection
	require.NoError(t, model.DB.Where("channel_id = ?", channelID).Find(&rows).Error)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, model.ChannelVeridropDetectionSkipped, row.Status)
	}
}

func TestRunVeridropDetectionTaskSingleChannelWithoutModelsIsSkipped(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.ChannelVeridropDetection{}))
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })
	setting.Enabled = true
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)

	channelID := 888000000 + int(time.Now().UnixNano()%1000000)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Name:    "veridrop-single-no-models",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-single-no-models",
		Status:  common.ChannelStatusEnabled,
		BaseURL: common.GetPointer("https://single.example/v1"),
		Group:   "default",
	}).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id = ?", channelID).Delete(&model.ChannelVeridropDetection{}).Error)
		require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
	})

	var progress [2]int
	summary := RunVeridropDetectionTask(context.Background(), VeridropDetectionTaskPayload{
		ChannelID: channelID,
		Model:     "   ",
	}, func(processed, total int) {
		progress = [2]int{processed, total}
	})

	require.Equal(t, 1, summary.Channels)
	require.Zero(t, summary.Models)
	require.Zero(t, summary.Created)
	require.Equal(t, 1, summary.Skipped)
	require.Equal(t, [2]int{1, 1}, progress)

	var rows []*model.ChannelVeridropDetection
	require.NoError(t, model.DB.Where("channel_id = ?", channelID).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, model.ChannelVeridropDetectionSkipped, rows[0].Status)
	require.Empty(t, rows[0].Model)
}

func TestFirstVeridropKey(t *testing.T) {
	require.Equal(t, "sk-first", firstVeridropKey("\n  sk-first  \n sk-second"))
	require.Empty(t, firstVeridropKey("\n \t "))
}

func TestSanitizeVeridropText(t *testing.T) {
	got := sanitizeVeridropText(
		"token sk-one and sk-two should be hidden, visible stays",
		"sk-one\nsk-two",
	)
	require.Equal(t, "token [redacted] and [redacted] should be hidden, visible stays", got)
}

func TestStartVeridropDetectionTaskRequiresEnabledMonitor(t *testing.T) {
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })

	setting.Enabled = false
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	_, _, err := StartVeridropDetectionTask(VeridropDetectionTaskPayload{Batch: true})
	require.ErrorIs(t, err, ErrVeridropMonitorDisabled)

	setting.Enabled = true
	setting.BaseURL = ""
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	_, _, err = StartVeridropDetectionTask(VeridropDetectionTaskPayload{Batch: true})
	require.ErrorIs(t, err, ErrVeridropBaseURLEmpty)
}

func TestVeridropTaskTypesIsolateSingleFromBatch(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}))
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })
	setting.Enabled = true
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)

	batch, created, err := StartVeridropDetectionTask(VeridropDetectionTaskPayload{Batch: true})
	require.NoError(t, err)
	require.True(t, created)
	single, singleCreated, err := StartSingleVeridropDetectionTask(VeridropDetectionTaskPayload{ChannelID: 1})
	require.NoError(t, err)
	require.True(t, singleCreated)
	require.Equal(t, model.SystemTaskTypeVeridrop, batch.Type)
	require.Equal(t, model.SystemTaskTypeVeridropSingle, single.Type)
	var storedPayload VeridropDetectionTaskPayload
	require.NoError(t, single.DecodePayload(&storedPayload))
	require.Equal(t, setting.DefaultOpenAIWireAPI, storedPayload.OpenAIWireAPI)
	t.Cleanup(func() {
		_ = model.DB.Where("task_id IN ?", []string{batch.TaskID, single.TaskID}).Delete(&model.SystemTask{}).Error
	})

	duplicate, duplicateCreated, err := StartSingleVeridropDetectionTask(VeridropDetectionTaskPayload{ChannelID: 2})
	require.NoError(t, err)
	require.False(t, duplicateCreated)
	require.Equal(t, single.TaskID, duplicate.TaskID)
}

func TestValidateVeridropWireAPI(t *testing.T) {
	for _, value := range []string{"", "chat_completions", "responses", " responses "} {
		require.NoError(t, validateVeridropWireAPI(value))
	}
	require.ErrorIs(t, validateVeridropWireAPI("invalid"), ErrInvalidVeridropWireAPI)
}

func TestVeridropBatchTaskIDIsStoredForCreatedAndSkippedRecords(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelVeridropDetection{}))
	channelID := 884000000 + int(time.Now().UnixNano()%1000000)
	channel := &model.Channel{
		Id:      channelID,
		Name:    "veridrop-batch-tag-test",
		Type:    constant.ChannelTypeOpenAI,
		Models:  "gpt-5",
		BaseURL: common.GetPointer("https://batch.example"),
	}
	batchTaskID := "systask_batch_tag_test"

	job, err := buildVeridropDetectionJob(channel, "gpt-5", "openai", "quick", batchTaskID, veridropSettingsSnapshot{})
	require.NoError(t, err)
	require.Equal(t, batchTaskID, job.Detection.BatchTaskID)

	require.NoError(t, createSkippedVeridropDetection(channel, "openai", "gpt-5", "quick", batchTaskID, "not applicable"))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id = ?", channelID).Delete(&model.ChannelVeridropDetection{}).Error)
	})

	var rows []*model.ChannelVeridropDetection
	require.NoError(t, model.DB.Where("channel_id = ?", channelID).Order("id asc").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, batchTaskID, rows[0].BatchTaskID)
	require.Equal(t, batchTaskID, rows[1].BatchTaskID)
}

func TestListVeridropDetectionTargets(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))

	baseID := 882000000
	ids := []int{baseID, baseID + 1, baseID + 2}
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id IN ?", ids).Delete(&model.Channel{}).Error)
	})

	channels := []*model.Channel{
		{
			Id:      ids[0],
			Name:    "veridrop-preview-openai",
			Type:    constant.ChannelTypeOpenAI,
			Key:     "sk-a",
			Status:  common.ChannelStatusEnabled,
			BaseURL: common.GetPointer("https://openai.example/v1/"),
			Models:  "gpt-5,gpt-4o,gpt-5",
			Group:   "default",
		},
		{
			Id:      ids[1],
			Name:    "veridrop-preview-midjourney",
			Type:    constant.ChannelTypeMidjourney,
			Key:     "sk-b",
			Status:  common.ChannelStatusEnabled,
			BaseURL: common.GetPointer("https://mj.example"),
			Models:  "mj",
			Group:   "default",
		},
		{
			Id:      ids[2],
			Name:    "veridrop-preview-empty-models",
			Type:    constant.ChannelTypeAnthropic,
			Key:     "sk-c",
			Status:  common.ChannelStatusEnabled,
			BaseURL: common.GetPointer("https://claude.example"),
			Group:   "default",
		},
	}
	for _, channel := range channels {
		require.NoError(t, model.DB.Save(channel).Error)
	}

	got, err := ListVeridropDetectionTargets(context.Background(), VeridropDetectionTaskPayload{
		ChannelIDs: ids,
	})
	require.NoError(t, err)
	require.Len(t, got.Items, 3)
	require.Equal(t, 1, got.ChannelCount)
	require.Equal(t, 2, got.ModelCount)
	require.Equal(t, 2, got.SkippedChannelCount)

	require.Equal(t, ids[0], got.Items[0].ChannelID)
	require.Equal(t, "OpenAI", got.Items[0].ChannelTypeName)
	require.Equal(t, "openai", got.Items[0].Protocol)
	require.Equal(t, "https://openai.example/v1", got.Items[0].BaseURL)
	require.Equal(t, []string{"gpt-5", "gpt-4o"}, got.Items[0].Models)
	require.Empty(t, got.Items[0].SkippedReason)
	require.Equal(t, common.ChannelStatusEnabled, got.Items[0].Status)

	require.Equal(t, "unsupported channel protocol", got.Items[1].SkippedReason)
	require.Equal(t, "channel has no enabled models", got.Items[2].SkippedReason)
}

func TestListVeridropDetectionTargetsIncludesDisabledWhenRequested(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))

	channelID := 884000000 + int(time.Now().UnixNano()%1000000)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
	})
	require.NoError(t, model.DB.Create(&model.Channel{
		Id: channelID, Name: "veridrop-disabled-preview", Type: constant.ChannelTypeOpenAI,
		Key: "sk-disabled", Status: common.ChannelStatusManuallyDisabled,
		BaseURL: common.GetPointer("https://disabled.example/v1"), Models: "gpt-5", Group: "default",
	}).Error)

	got, err := ListVeridropDetectionTargets(context.Background(), VeridropDetectionTaskPayload{
		ChannelIDs:      []int{channelID},
		IncludeDisabled: true,
	})
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.Equal(t, common.ChannelStatusManuallyDisabled, got.Items[0].Status)
}

func TestRunVeridropBatchDetectionCanExplicitlyIncludeDisabledChannel(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.ChannelVeridropDetection{}))
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })
	setting.Enabled = true
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)

	channelID := 885000000 + int(time.Now().UnixNano()%1000000)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id = ?", channelID).Delete(&model.ChannelVeridropDetection{}).Error)
		require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
	})
	require.NoError(t, model.DB.Create(&model.Channel{
		Id: channelID, Name: "veridrop-disabled-explicit", Type: constant.ChannelTypeOpenAI,
		Key: "sk-disabled", Status: common.ChannelStatusManuallyDisabled,
		BaseURL: common.GetPointer("https://disabled.example/v1"), Group: "default",
	}).Error)

	excluded := RunVeridropDetectionTask(context.Background(), VeridropDetectionTaskPayload{
		Batch: true, ChannelIDs: []int{channelID},
	}, nil)
	require.Zero(t, excluded.Channels)
	require.Zero(t, excluded.Skipped)

	included := RunVeridropDetectionTask(context.Background(), VeridropDetectionTaskPayload{
		Batch: true, ChannelIDs: []int{channelID}, IncludeDisabled: true,
	}, nil)
	require.Equal(t, 1, included.Channels)
	require.Equal(t, 1, included.Skipped)
}

func TestStartManualVeridropDetection(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelVeridropDetection{}))

	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })

	var sawSubmit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/detect/openai":
			sawSubmit = true
			require.NoError(t, r.ParseForm())
			require.Equal(t, "https://manual.example/v1", r.Form.Get("base_url"))
			require.Equal(t, "sk-manual", r.Form.Get("api_key"))
			require.Equal(t, "gpt-5", r.Form.Get("model"))
			require.Equal(t, "quick", r.Form.Get("mode"))
			_, _ = w.Write([]byte(`{"job_id":"manual-job"}`))
		case "/api/status/manual-job":
			_, _ = w.Write([]byte(`{"job_id":"manual-job","status":"done"}`))
		case "/api/result/manual-job.json":
			_, _ = w.Write([]byte(`{"protocol":"openai","total_score":91,"verdict":"passed","summary":"ok","run_error":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setting.Enabled = true
	setting.BaseURL = server.URL
	setting.DefaultMode = "quick"
	setting.SubmitTimeoutSeconds = 3
	setting.PollIntervalSeconds = 1
	setting.JobTimeoutSeconds = 5
	operation_setting.ReplaceVeridropMonitorSetting(setting)

	detection, err := StartManualVeridropDetection(context.Background(), VeridropManualDetectionPayload{
		BaseURL:  "https://manual.example/v1/",
		APIKey:   "sk-manual",
		Model:    "gpt-5",
		Protocol: "openai",
	})
	require.NoError(t, err)
	require.NotZero(t, detection.ID)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id = ?", detection.ID).Delete(&model.ChannelVeridropDetection{}).Error)
	})

	require.Eventually(t, func() bool {
		got, err := model.GetChannelVeridropDetectionByID(detection.ID)
		require.NoError(t, err)
		return got != nil && got.Status == model.ChannelVeridropDetectionDone
	}, 5*time.Second, 100*time.Millisecond)

	got, err := model.GetChannelVeridropDetectionByID(detection.ID)
	require.NoError(t, err)
	require.True(t, sawSubmit)
	require.Equal(t, 0, got.ChannelID)
	require.Equal(t, "Manual Test", got.ChannelName)
	require.Equal(t, "openai", got.Protocol)
	require.Equal(t, "gpt-5", got.Model)
	require.Equal(t, float64(91), got.Score)
}

func TestRunVeridropDetectionJobDoesNotChangeChannelStatus(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelVeridropDetection{}))

	channelID := 886000000 + int(time.Now().UnixNano()%1000000)
	channel := &model.Channel{
		Id:      channelID,
		Name:    "veridrop-record-only",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-record-only",
		Status:  common.ChannelStatusEnabled,
		BaseURL: common.GetPointer("https://record-only.example/v1"),
		Models:  "gpt-5",
		Group:   "default",
	}
	require.NoError(t, model.DB.Create(channel).Error)
	detection := &model.ChannelVeridropDetection{
		ChannelID: channelID,
		Status:    model.ChannelVeridropDetectionQueued,
	}
	require.NoError(t, model.CreateChannelVeridropDetection(detection))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id = ?", detection.ID).Delete(&model.ChannelVeridropDetection{}).Error)
		require.NoError(t, model.DB.Where("id = ?", channelID).Delete(&model.Channel{}).Error)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/detect/openai":
			_, _ = w.Write([]byte(`{"job_id":"record-only-job"}`))
		case "/api/status/record-only-job":
			_, _ = w.Write([]byte(`{"job_id":"record-only-job","status":"done"}`))
		case "/api/result/record-only-job.json":
			_, _ = w.Write([]byte(`{"total_score":0,"verdict":"failed","summary":"unavailable"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	summary := &VeridropDetectionSummary{}
	runVeridropDetectionJob(
		context.Background(),
		veridropDetectionJob{
			Channel:   channel,
			Protocol:  "openai",
			Model:     "gpt-5",
			Mode:      "quick",
			Detection: detection,
			APIKey:    channel.Key,
		},
		VeridropDetectionTaskPayload{},
		veridropSettingsSnapshot{
			BaseURL:              server.URL,
			SubmitTimeoutSeconds: 2,
			PollIntervalSeconds:  1,
			JobTimeoutSeconds:    2,
		},
		summary,
	)

	reloaded, err := model.GetChannelById(channelID, true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	require.Equal(t, 1, summary.Succeeded)
}

func TestVeridropClientSubmitPollAndFetch(t *testing.T) {
	var sawSubmit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/detect/openai":
			sawSubmit = true
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "Bearer admin-token", r.Header.Get("Authorization"))
			require.NoError(t, r.ParseForm())
			require.Equal(t, "https://upstream.example/v1", r.Form.Get("base_url"))
			require.Equal(t, "sk-live", r.Form.Get("api_key"))
			require.Equal(t, "gpt-5", r.Form.Get("model"))
			require.Equal(t, "standard", r.Form.Get("mode"))
			require.Equal(t, "true", r.Form.Get("include_long_context"))
			require.Equal(t, "false", r.Form.Get("include_long_context_extreme"))
			require.Equal(t, "responses", r.Form.Get("wire_api"))
			require.Equal(t, "true", r.Form.Get("force"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"job_id":"job-1","status_url":"/api/status/job-1"}`))
		case "/api/status/job-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"job_id":"job-1","protocol":"openai","status":"done"}`))
		case "/api/result/job-1.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"protocol":"openai","total_score":88.5,"verdict":"passed","summary":"ready","run_error":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	baseURL := "https://upstream.example/v1/"
	settings := veridropSettingsSnapshot{
		BaseURL:              server.URL,
		AdminToken:           "admin-token",
		SubmitTimeoutSeconds: 2,
		PollIntervalSeconds:  1,
		JobTimeoutSeconds:    2,
	}
	job := veridropDetectionJob{
		Channel: &model.Channel{
			Type:    constant.ChannelTypeOpenAI,
			Name:    "test-channel",
			BaseURL: common.GetPointer(baseURL),
		},
		Protocol: "openai",
		Model:    "gpt-5",
		Mode:     "standard",
	}
	payload := VeridropDetectionTaskPayload{
		IncludeLongContext: common.GetPointer(true),
		OpenAIWireAPI:      "responses",
		Force:              true,
	}

	submitResp, err := submitVeridropDetection(context.Background(), settings, job, "sk-live", payload)
	require.NoError(t, err)
	require.True(t, sawSubmit)
	require.Equal(t, "job-1", submitResp.JobID)

	status, err := pollVeridropDetection(context.Background(), settings, submitResp.JobID)
	require.NoError(t, err)
	require.Equal(t, "done", status.Status)

	body, report, err := fetchVeridropResult(context.Background(), settings, submitResp.JobID)
	require.NoError(t, err)
	require.Contains(t, body, `"total_score":88.5`)
	require.Equal(t, "openai", report.Protocol)
	require.Equal(t, 88.5, report.Score)
	require.Equal(t, "passed", report.Verdict)
}

func TestSubmitVeridropDetectionOmitsUnsupportedGeminiFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/detect/gemini", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "", r.Form.Get("include_long_context"))
		require.Equal(t, "", r.Form.Get("include_long_context_extreme"))
		require.Equal(t, "", r.Form.Get("wire_api"))
		require.Equal(t, "", r.Form.Get("force"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"job-gemini"}`))
	}))
	defer server.Close()

	baseURL := "https://generativelanguage.googleapis.com"
	job := veridropDetectionJob{
		Channel:  &model.Channel{BaseURL: common.GetPointer(baseURL)},
		Protocol: "gemini",
		Model:    "gemini-2.5-pro",
		Mode:     "standard",
	}
	payload := VeridropDetectionTaskPayload{
		IncludeLongContext:        common.GetPointer(true),
		IncludeLongContextExtreme: common.GetPointer(true),
		OpenAIWireAPI:             "responses",
	}

	response, err := submitVeridropDetection(context.Background(), veridropSettingsSnapshot{
		BaseURL:              server.URL,
		SubmitTimeoutSeconds: 2,
	}, job, "gemini-key", payload)
	require.NoError(t, err)
	require.Equal(t, "job-gemini", response.JobID)
}

func TestSubmitVeridropDetectionRejectsInvalidOpenAIWireAPI(t *testing.T) {
	job := veridropDetectionJob{
		Channel:  &model.Channel{BaseURL: common.GetPointer("https://api.example.com/v1")},
		Protocol: "openai",
		Model:    "gpt-5",
		Mode:     "quick",
	}
	_, err := submitVeridropDetection(context.Background(), veridropSettingsSnapshot{
		BaseURL:              "https://veridrop.invalid",
		SubmitTimeoutSeconds: 2,
	}, job, "sk-test", VeridropDetectionTaskPayload{OpenAIWireAPI: "invalid"})
	require.ErrorIs(t, err, ErrInvalidVeridropWireAPI)
}

func TestVeridropClientHandlesHTTPErrorAndTimeout(t *testing.T) {
	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad key sk-secret", http.StatusUnauthorized)
	}))
	defer errorServer.Close()

	settings := veridropSettingsSnapshot{BaseURL: errorServer.URL, SubmitTimeoutSeconds: 2}
	job := veridropDetectionJob{
		Channel:  &model.Channel{BaseURL: common.GetPointer("https://upstream.example")},
		Protocol: "openai",
		Model:    "gpt-5",
		Mode:     "quick",
	}
	_, err := submitVeridropDetection(context.Background(), settings, job, "sk-secret", VeridropDetectionTaskPayload{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP 401")
	require.True(t, strings.Contains(err.Error(), "bad key"))

	runningServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"job-2","status":"running"}`))
	}))
	defer runningServer.Close()

	settings = veridropSettingsSnapshot{
		BaseURL:              runningServer.URL,
		SubmitTimeoutSeconds: 2,
		PollIntervalSeconds:  1,
		JobTimeoutSeconds:    1,
	}
	_, err = pollVeridropDetection(context.Background(), settings, "job-2")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestVeridropClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", veridropMaxResponseBytes+1)))
	}))
	defer server.Close()

	_, err := doVeridropRequest(
		context.Background(),
		veridropSettingsSnapshot{},
		http.MethodGet,
		server.URL,
		nil,
		2,
	)
	require.ErrorIs(t, err, ErrVeridropResponseTooLarge)
}

func TestVeridropClientBoundsHTTPErrorDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("private-detail-", 1000)))
	}))
	defer server.Close()

	_, err := doVeridropRequest(
		context.Background(),
		veridropSettingsSnapshot{},
		http.MethodGet,
		server.URL,
		nil,
		2,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP 502")
	require.Less(t, len(err.Error()), veridropMaxErrorDetailBytes+100)
}
