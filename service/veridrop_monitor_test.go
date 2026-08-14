package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestNormalizeVeridropModelNames(t *testing.T) {
	require.Equal(
		t,
		[]string{"claude-haiku-4-5", "gpt-5"},
		normalizeVeridropModelNames([]string{" claude-haiku-4-5 ", "", "gpt-5", "gpt-5"}),
	)
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

func TestShouldAutoDisableByVeridrop(t *testing.T) {
	settings := veridropSettingsSnapshot{
		AutoDisableEnabled:         true,
		AutoDisableFailedThreshold: 40,
	}

	require.True(t, shouldAutoDisableByVeridrop(veridropReportSummary{Verdict: "failed", Score: 40}, settings))
	require.False(t, shouldAutoDisableByVeridrop(veridropReportSummary{Verdict: "failed", Score: 41}, settings))
	require.False(t, shouldAutoDisableByVeridrop(veridropReportSummary{Verdict: "passed", Score: 10}, settings))
	settings.AutoDisableEnabled = false
	require.False(t, shouldAutoDisableByVeridrop(veridropReportSummary{Verdict: "failed", Score: 10}, settings))
}

func TestStartVeridropDetectionTaskRequiresEnabledMonitor(t *testing.T) {
	setting := operation_setting.GetVeridropMonitorSetting()
	original := *setting
	t.Cleanup(func() { *setting = original })

	setting.Enabled = false
	setting.BaseURL = "https://veridrop.example"
	_, _, err := StartVeridropDetectionTask(VeridropDetectionTaskPayload{Batch: true})
	require.ErrorIs(t, err, ErrVeridropMonitorDisabled)

	setting.Enabled = true
	setting.BaseURL = ""
	_, _, err = StartVeridropDetectionTask(VeridropDetectionTaskPayload{Batch: true})
	require.ErrorIs(t, err, ErrVeridropBaseURLEmpty)
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

	require.Equal(t, "unsupported channel protocol", got.Items[1].SkippedReason)
	require.Equal(t, "channel has no enabled models", got.Items[2].SkippedReason)
}

func TestStartManualVeridropDetection(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelVeridropDetection{}))

	setting := operation_setting.GetVeridropMonitorSetting()
	original := *setting
	t.Cleanup(func() { *setting = original })

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
		IncludeLongContext: true,
		OpenAIWireAPI:      "responses",
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
