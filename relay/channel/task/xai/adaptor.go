package xai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.apiKey = info.ApiKey
	a.baseURL = info.ChannelBaseUrl
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionTextGenerate)
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := a.baseURL
	if baseURL == "" && info != nil && info.ChannelMeta != nil {
		baseURL = info.ChannelBaseUrl
	}
	return fmt.Sprintf("%s/v1/videos/generations", strings.TrimRight(baseURL, "/")), nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	apiKey := a.apiKey
	if apiKey == "" && info != nil && info.ChannelMeta != nil {
		apiKey = info.ApiKey
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	meta := &requestMetadata{}
	_ = taskcommon.UnmarshalMetadata(req.Metadata, meta)

	modelName := ""
	if info != nil {
		modelName = info.UpstreamModelName
		if modelName == "" {
			modelName = info.OriginModelName
		}
	}
	seconds := resolveDurationSeconds(req.Metadata, req.Duration, req.Seconds)
	resolution := resolveResolution(req.Metadata, req.Size)
	supported := supportedResolution(modelName, resolution)
	imageCount := imageInputCount(req, meta)
	if info != nil {
		if info.PriceData.PricingMetadata == nil {
			info.PriceData.PricingMetadata = make(map[string]string)
		}
		info.PriceData.PricingMetadata["xai_video_model"] = modelName
		info.PriceData.PricingMetadata["duration_seconds"] = strconv.Itoa(seconds)
		info.PriceData.PricingMetadata["requested_resolution"] = resolution
		info.PriceData.PricingMetadata["resolution"] = supported
		info.PriceData.PricingMetadata["reference_image_count"] = strconv.Itoa(imageCount)
	}
	return map[string]float64{
		"xai_video_units": xaiVideoUnits(modelName, supported, seconds, imageCount),
	}
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	meta := &requestMetadata{}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, meta); err != nil {
		return nil, fmt.Errorf("unmarshal metadata failed: %w", err)
	}

	duration := resolveDurationSeconds(req.Metadata, req.Duration, req.Seconds)
	if meta.Duration != nil && *meta.Duration > 0 {
		duration = min(*meta.Duration, relaycommon.MaxTaskDurationSeconds)
	}
	if meta.DurationSeconds != nil && *meta.DurationSeconds > 0 {
		duration = min(*meta.DurationSeconds, relaycommon.MaxTaskDurationSeconds)
	}

	aspectRatio := resolveAspectRatio(req.Metadata, req.Size)
	if strings.TrimSpace(meta.AspectRatio) != "" {
		aspectRatio = strings.TrimSpace(meta.AspectRatio)
	}

	resolution := resolveResolution(req.Metadata, req.Size)
	if strings.TrimSpace(meta.Resolution) != "" {
		resolution = normalizeResolution(meta.Resolution)
	}

	body := videoGenerationRequest{
		Model:       info.UpstreamModelName,
		Prompt:      req.Prompt,
		Duration:    duration,
		AspectRatio: aspectRatio,
		Seed:        meta.Seed,
	}
	if body.Model == "" {
		body.Model = info.OriginModelName
	}
	body.Resolution = supportedResolution(body.Model, resolution)
	if image := firstRequestImage(c, req, meta, info); image != nil {
		body.Image = image
		info.Action = constant.TaskActionGenerate
	}
	if refs := requestReferenceImages(req, meta); len(refs) > 0 {
		body.ReferenceImages = refs
		info.Action = constant.TaskActionGenerate
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	var s submitResponse
	if err := common.Unmarshal(responseBody, &s); err != nil {
		return "", nil, service.TaskErrorWrapper(err, "unmarshal_response_failed", http.StatusInternalServerError)
	}
	if strings.TrimSpace(s.RequestID) == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("missing request_id"), "invalid_response", http.StatusInternalServerError)
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)

	return s.RequestID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/videos/%s", strings.TrimRight(baseUrl, "/"), taskID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var sr statusResponse
	if err := common.Unmarshal(respBody, &sr); err != nil {
		return nil, fmt.Errorf("unmarshal status response failed: %w", err)
	}

	ti := &relaycommon.TaskInfo{}
	status := strings.ToLower(strings.TrimSpace(sr.Status))
	if status == "" && sr.Error != nil {
		status = "error"
	}
	switch status {
	case "", "processing", "in_progress", "generating":
		ti.Status = model.TaskStatusInProgress
		ti.Progress = taskcommon.ProgressInProgress
	case "queued", "pending":
		ti.Status = model.TaskStatusQueued
		ti.Progress = taskcommon.ProgressQueued
	case "done", "completed", "success":
		ti.Status = model.TaskStatusSuccess
		ti.Progress = taskcommon.ProgressComplete
		if sr.Video != nil {
			ti.Url = strings.TrimSpace(sr.Video.URL)
		}
	case "expired":
		ti.Status = model.TaskStatusFailure
		ti.Progress = taskcommon.ProgressComplete
		ti.Reason = "task expired"
	case "failed", "error":
		ti.Status = model.TaskStatusFailure
		ti.Progress = taskcommon.ProgressComplete
		if sr.Error != nil && strings.TrimSpace(sr.Error.Message) != "" {
			ti.Reason = strings.TrimSpace(sr.Error.Message)
		} else {
			ti.Reason = "task failed"
		}
	default:
		return nil, fmt.Errorf("unknown xai video task status: %s", sr.Status)
	}
	return ti, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	video := dto.NewOpenAIVideo()
	video.ID = task.TaskID
	video.TaskID = task.TaskID
	video.Model = task.Properties.OriginModelName
	video.Status = task.Status.ToVideoStatus()
	video.SetProgressStr(task.Progress)
	video.CreatedAt = task.CreatedAt
	if task.FinishTime > 0 {
		video.CompletedAt = task.FinishTime
	} else if task.UpdatedAt > 0 {
		video.CompletedAt = task.UpdatedAt
	}
	if resultURL := strings.TrimSpace(task.GetResultURL()); resultURL != "" && task.Status == model.TaskStatusSuccess {
		video.SetMetadata("url", resultURL)
	}
	if task.Status == model.TaskStatusFailure && strings.TrimSpace(task.FailReason) != "" {
		video.Error = &dto.OpenAIVideoError{
			Message: task.FailReason,
			Code:    "task_failed",
		}
	}
	return common.Marshal(video)
}

func (a *TaskAdaptor) GetModelList() []string {
	return []string{"grok-imagine-video", "grok-imagine-video-1.5"}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "xai"
}
