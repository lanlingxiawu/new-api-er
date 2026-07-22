package thirdpartysd2

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
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	Resolution  string         `json:"resolution,omitempty"`
	Ratio       string         `json:"ratio,omitempty"`
	Duration    *dto.IntValue  `json:"duration,omitempty"`
	Frames      *dto.IntValue  `json:"frames,omitempty"`
	Seed        *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark   *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	Task *responseTaskData `json:"task"`
}

type responseTask struct {
	Task *responseTaskData `json:"task"`
}

type responseTaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *responseTaskError) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*e = responseTaskError{}
		return nil
	}

	// Upstream may return either a plain string or an object for task.error.
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var message string
		if err := common.Unmarshal(trimmed, &message); err != nil {
			return err
		}
		*e = responseTaskError{Message: message}
		return nil
	}

	type alias responseTaskError
	var parsed alias
	if err := common.Unmarshal(trimmed, &parsed); err != nil {
		return err
	}
	*e = responseTaskError(parsed)
	return nil
}

func (e *responseTaskError) reason() string {
	if e == nil {
		return ""
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		return message
	}
	return strings.TrimSpace(e.Code)
}

type responseTaskData struct {
	ID              string   `json:"id"`
	Model           string   `json:"model"`
	Status          string   `json:"status"`
	Outputs         []string `json:"outputs"`
	DurationSeconds int      `json:"duration_seconds"`
	Ratio           string   `json:"ratio"`
	Usage           struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error       *responseTaskError `json:"error"`
	CreatedAt   string             `json:"created_at"`
	CompletedAt *string            `json:"completed_at"`
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

type resolvedThirdPartySD2PricingContext struct {
	ModelName     string
	Resolution    string
	HasVideoInput bool
}

const resolvedThirdPartySD2PricingContextKey = "thirdpartysd2_pricing_context"

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapper(err, "get_task_request_failed", http.StatusBadRequest)
	}

	modelName := resolveThirdPartySD2ModelName(info, &req)
	if modelName == "" {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("thirdpartysd2 请求缺少模型名称，无法匹配定价"),
			"missing_model",
			http.StatusBadRequest,
		)
	}
	inheritedPricingMetadata := getInheritedThirdPartySD2PricingMetadata(info)
	resolution := resolveThirdPartySD2Resolution(&req, inheritedPricingMetadata)
	if resolution == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("thirdpartysd2 请求缺少可识别的分辨率，无法匹配定价"), "invalid_resolution", http.StatusBadRequest)
	}
	hasVideoInput := resolveThirdPartySD2HasVideoInput(&req, inheritedPricingMetadata)
	if _, ok := model_setting.GetThirdPartySD2TokenPrice(modelName, resolution, hasVideoInput); !ok {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("模型 %s 在分辨率 %s 下无定价", modelName, resolution),
			"resolution_pricing_not_found",
			http.StatusBadRequest,
		)
	}
	c.Set(resolvedThirdPartySD2PricingContextKey, resolvedThirdPartySD2PricingContext{
		ModelName:     modelName,
		Resolution:    resolution,
		HasVideoInput: hasVideoInput,
	})
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/v1/video/generate", a.baseURL), nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	modelName, resolution, hasVideoInput, ok := getResolvedThirdPartySD2PricingContext(c, info)
	if !ok {
		return nil
	}
	unitPrice, ok := model_setting.GetThirdPartySD2TokenPrice(modelName, resolution, hasVideoInput)
	if !ok {
		return nil
	}

	groupRatio := info.PriceData.GroupRatioInfo.GroupRatio
	info.PriceData.UsePrice = false
	info.PriceData.ModelPrice = -1
	info.PriceData.ModelRatio = model_setting.ThirdPartySD2PriceToModelRatio(unitPrice)
	info.PriceData.Quota = model_setting.CalculateThirdPartySD2Quota(
		unitPrice,
		model_setting.ThirdPartySD2PreConsumedTokenEstimate,
		groupRatio,
	)
	info.PriceData.FreeModel = false
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		if groupRatio == 0 || unitPrice == 0 {
			info.PriceData.Quota = 0
			info.PriceData.FreeModel = true
		}
	}
	info.PriceData.PricingMetadata = map[string]string{
		"channel":      ChannelName,
		"pricing_mode": "resolution_video_matrix",
		"resolution":   resolution,
		"video_input":  strconv.FormatBool(hasVideoInput),
	}
	return nil
}

func getResolvedThirdPartySD2PricingContext(c *gin.Context, info *relaycommon.RelayInfo) (string, string, bool, bool) {
	if c != nil {
		if value, exists := c.Get(resolvedThirdPartySD2PricingContextKey); exists {
			if resolved, ok := value.(resolvedThirdPartySD2PricingContext); ok {
				return resolved.ModelName, resolved.Resolution, resolved.HasVideoInput, true
			}
		}
	}
	if c == nil {
		return "", "", false, false
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return "", "", false, false
	}
	inheritedPricingMetadata := getInheritedThirdPartySD2PricingMetadata(info)
	resolution := resolveThirdPartySD2Resolution(&req, inheritedPricingMetadata)
	if resolution == "" {
		return "", "", false, false
	}
	modelName := resolveThirdPartySD2ModelName(info, &req)
	if modelName == "" {
		return "", "", false, false
	}
	hasVideoInput := resolveThirdPartySD2HasVideoInput(&req, inheritedPricingMetadata)
	return modelName, resolution, hasVideoInput, true
}

func hasVideoInMetadata(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentRaw, ok := metadata["content"]
	if !ok {
		return false
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

func getInheritedThirdPartySD2PricingMetadata(info *relaycommon.RelayInfo) map[string]string {
	if info == nil || info.TaskRelayInfo == nil {
		return nil
	}
	return info.TaskRelayInfo.InheritedPricingMetadata
}

func resolveThirdPartySD2ModelName(info *relaycommon.RelayInfo, req *relaycommon.TaskSubmitReq) string {
	if req != nil {
		if modelName := strings.TrimSpace(req.Model); modelName != "" {
			return modelName
		}
	}
	if info == nil {
		return ""
	}
	return strings.TrimSpace(info.OriginModelName)
}

func resolveThirdPartySD2HasVideoInput(req *relaycommon.TaskSubmitReq, inheritedPricingMetadata map[string]string) bool {
	if req != nil && hasVideoInMetadata(req.Metadata) {
		return true
	}
	if inheritedPricingMetadata == nil {
		return false
	}
	hasVideoInput, err := strconv.ParseBool(strings.TrimSpace(inheritedPricingMetadata["video_input"]))
	return err == nil && hasVideoInput
}

func resolveThirdPartySD2Resolution(req *relaycommon.TaskSubmitReq, inheritedPricingMetadata map[string]string) string {
	inheritedResolution := ""
	if inheritedPricingMetadata != nil {
		inheritedResolution = inheritedPricingMetadata["resolution"]
	}
	if req == nil {
		return model_setting.MaxThirdPartySD2Resolution(inheritedResolution)
	}
	metadataResolution := ""
	if req.Metadata != nil {
		metadataResolution = common.Interface2String(req.Metadata["resolution"])
	}
	// Only compare values explicitly declared on the current request. Inherited
	// resolution is used as a fallback for remix/retry requests that omit both.
	if currentResolution := model_setting.MaxThirdPartySD2Resolution(metadataResolution, req.Size); currentResolution != "" {
		return currentResolution
	}
	return model_setting.MaxThirdPartySD2Resolution(inheritedResolution)
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
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

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}

	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.Task == nil || dResp.Task.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.Task.ID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/v1/video/tasks/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, error) {
	modelName := resolveThirdPartySD2ModelName(info, req)
	r := requestPayload{
		Model:   modelName,
		Content: []ContentItem{},
	}

	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	if resolvedResolution := resolveThirdPartySD2Resolution(req, getInheritedThirdPartySD2PricingMetadata(info)); resolvedResolution != "" {
		r.Resolution = resolvedResolution
	}

	duration := req.Duration
	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		duration = sec
	}
	if duration > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(duration))
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	return &r, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}
	if resTask.Task == nil {
		return nil, errors.New("task is empty")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	switch resTask.Task.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running", "in_progress":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "completed", "succeeded", "success":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = firstOutputURL(resTask.Task.Outputs)
		taskResult.CompletionTokens = resTask.Task.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Task.Usage.TotalTokens
	case "failed", "error", "cancelled":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		if reason := resTask.Task.Error.reason(); reason != "" {
			taskResult.Reason = reason
		} else {
			taskResult.Reason = "task failed"
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func firstOutputURL(outputs []string) string {
	for _, output := range outputs {
		if output != "" {
			return output
		}
	}
	return ""
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal third-party sd2 task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	if originTask.Status == model.TaskStatusSuccess && hasUsableUpstreamResultURL(originTask) {
		openAIVideo.SetMetadata("url", taskcommon.BuildProxyURL(originTask.TaskID))
	}
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Task != nil && (dResp.Task.Status == "failed" || dResp.Task.Status == "error" || dResp.Task.Status == "cancelled") {
		errorMessage := originTask.FailReason
		errorCode := "task_failed"
		if dResp.Task.Error != nil {
			if reason := dResp.Task.Error.reason(); reason != "" {
				errorMessage = reason
			}
			if code := strings.TrimSpace(dResp.Task.Error.Code); code != "" {
				errorCode = code
			}
		}
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: errorMessage,
			Code:    errorCode,
		}
	}

	return common.Marshal(openAIVideo)
}

func hasUsableUpstreamResultURL(task *model.Task) bool {
	if task == nil {
		return false
	}
	url := strings.TrimSpace(task.GetResultURL())
	if url == "" {
		return false
	}
	return !strings.Contains(url, "/v1/videos/"+task.TaskID+"/content")
}
