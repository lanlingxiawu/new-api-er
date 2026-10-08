package ali

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func oaiImage2AliImageRequest(info *relaycommon.RelayInfo, request dto.ImageRequest, isSync bool) (*AliImageRequest, error) {
	var imageRequest AliImageRequest
	imageRequest.Model = request.Model
	imageRequest.ResponseFormat = request.ResponseFormat
	imageRequest.Parameters = AliImageParameters{
		Size:      strings.ReplaceAll(request.Size, "x", "*"),
		Watermark: request.Watermark,
	}
	if request.Extra != nil {
		if val, ok := request.Extra["parameters"]; ok {
			imageRequest.Parameters = AliImageParameters{}
			err := common.Unmarshal(val, &imageRequest.Parameters)
			if err != nil {
				return nil, fmt.Errorf("invalid parameters field: %w", err)
			}
			// Direct adaptor callers can bypass ingress validation. Reuse the
			// decoded provider scalars and the same quantity validation below.
			if request.BillingParameters == nil {
				request.BillingParameters = &dto.ImageBillingParameters{
					N: imageRequest.Parameters.N, PromptExtend: imageRequest.Parameters.PromptExtend,
				}
			}
		}
		if val, ok := request.Extra["input"]; ok {
			err := common.Unmarshal(val, &imageRequest.Input)
			if err != nil {
				return nil, fmt.Errorf("invalid input field: %w", err)
			}
		}
	}

	count, err := request.ImageCount(true)
	if err != nil {
		return nil, err
	}
	imageRequest.Parameters.N = common.GetPointer(uint(count))
	if request.BillingParameters != nil {
		imageRequest.Parameters.PromptExtend = request.BillingParameters.PromptExtend
	}
	if info.TieredBillingSnapshot == nil {
		info.PriceData.AddOtherRatio("n", float64(count))
		if strings.Contains(request.Model, "z-image") && imageRequest.Parameters.PromptExtendValue() {
			info.PriceData.AddOtherRatio("prompt_extend", common.ZImagePromptExtendMultiplier)
		}
	}

	// 同步图片模型和异步图片模型请求格式不一样
	if isSync {
		if imageRequest.Input == nil {
			imageRequest.Input = AliImageInput{
				Messages: []AliMessage{
					{
						Role: "user",
						Content: []AliMediaContent{
							{
								Text: request.Prompt,
							},
						},
					},
				},
			}
		}
	} else {
		if imageRequest.Input == nil {
			imageRequest.Input = AliImageInput{
				Prompt: request.Prompt,
			}
		}
	}

	return &imageRequest, nil
}
func getImageBase64sFromForm(c *gin.Context, fieldName string) ([]string, error) {
	mf := c.Request.MultipartForm
	if mf == nil {
		if _, err := c.MultipartForm(); err != nil {
			return nil, fmt.Errorf("failed to parse image edit form request: %w", err)
		}
		mf = c.Request.MultipartForm
	}

	var imageFiles []*multipart.FileHeader
	var exists bool

	// First check for standard "image" field
	if imageFiles, exists = mf.File["image"]; !exists || len(imageFiles) == 0 {
		// If not found, check for "image[]" field
		if imageFiles, exists = mf.File["image[]"]; !exists || len(imageFiles) == 0 {
			// If still not found, iterate through all fields to find any that start with "image["
			foundArrayImages := false
			for fieldName, files := range mf.File {
				if strings.HasPrefix(fieldName, "image[") && len(files) > 0 {
					foundArrayImages = true
					imageFiles = append(imageFiles, files...)
				}
			}

			// If no image fields found at all
			if !foundArrayImages && (len(imageFiles) == 0) {
				return nil, errors.New("image is required")
			}
		}
	}

	if len(imageFiles) == 0 {
		return nil, errors.New("image is required")
	}

	//if len(imageFiles) > 1 {
	//	return nil, errors.New("only one image is supported for qwen edit")
	//}

	// 获取base64编码的图片
	var imageBase64s []string
	for _, file := range imageFiles {
		image, err := file.Open()
		if err != nil {
			return nil, errors.New("failed to open image file")
		}

		// 读取文件内容
		imageData, err := io.ReadAll(image)
		if err != nil {
			return nil, errors.New("failed to read image file")
		}

		// 获取MIME类型
		mimeType := http.DetectContentType(imageData)

		// 编码为base64
		base64Data := base64.StdEncoding.EncodeToString(imageData)

		// 构造data URL格式
		dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data)
		imageBase64s = append(imageBase64s, dataURL)
		image.Close()
	}
	return imageBase64s, nil
}

func oaiFormEdit2AliImageEdit(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (*AliImageRequest, error) {
	count, err := request.ImageCount(true)
	if err != nil {
		return nil, err
	}
	var imageRequest AliImageRequest
	imageRequest.Model = request.Model
	imageRequest.ResponseFormat = request.ResponseFormat

	imageBase64s, err := getImageBase64sFromForm(c, "image")
	if err != nil {
		return nil, fmt.Errorf("get image base64s from form failed: %w", err)
	}
	//dto.MediaContent{}
	mediaContents := make([]AliMediaContent, len(imageBase64s))
	for i, b64 := range imageBase64s {
		mediaContents[i] = AliMediaContent{
			Image: b64,
		}
	}
	mediaContents = append(mediaContents, AliMediaContent{
		Text: request.Prompt,
	})
	imageRequest.Input = AliImageInput{
		Messages: []AliMessage{
			{
				Role:    "user",
				Content: mediaContents,
			},
		},
	}
	imageRequest.Parameters = AliImageParameters{
		N:         common.GetPointer(uint(count)),
		Watermark: request.Watermark,
	}
	if request.BillingParameters != nil {
		imageRequest.Parameters.PromptExtend = request.BillingParameters.PromptExtend
	}
	return &imageRequest, nil
}

// updateTask 查询 taskID 的原始任务响应；c/info 提供请求上下文与会话，ctx 为轮询的上游 context，返回解析结果、错误及本次原始正文。
// 受管图片轮询延续初始 HTTP 200 的会话，采集后再解析，不重置门控/证据；非受管请求保持既有行为。
func updateTask(c *gin.Context, info *relaycommon.RelayInfo, taskID string, ctx context.Context) (*AliResponse, error, []byte) {
	url := fmt.Sprintf("%s/api/v1/tasks/%s", info.ChannelBaseUrl, taskID)

	var aliResponse AliResponse

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return &aliResponse, err, nil
	}

	req.Header.Set("Authorization", "Bearer "+info.ApiKey)

	managed := info.StreamSession.Active() && info.StreamSession.ExpectedImages > 0
	// 轮询的是已提交（上游已计费）的任务：只受 asyncTaskWait 的上游 context（我方时限与请求结束）约束，
	// 客户端离开不中断——受管图片会话也一样。
	req = req.WithContext(ctx)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		if managed {
			info.StreamSession.Fail("upstream_read_error", err)
			return &aliResponse, err, nil
		}
		common.SysLog("updateTask client.Do err: " + err.Error())
		return &aliResponse, err, nil
	}
	if managed {
		// 轮询不是新的渠道尝试；直接观察本次响应，避免 ObserveTransport 清空前次已确认用量。
		info.StreamDiagnostic.Observe(resp)
		info.StreamSession.ObserveHTTP(resp)
		relaycommon.UseStreamImageTaskResponse(resp)
	}
	defer resp.Body.Close() // 包装完成后登记 Close，保证采集器与会话共用同一个幂等关闭入口。

	responseBody, err := io.ReadAll(resp.Body)
	if managed {
		if err != nil {
			info.StreamSession.Fail("upstream_read_error", err)
			return &aliResponse, err, responseBody
		}
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("upstream image task returned HTTP %d", resp.StatusCode)
			info.StreamSession.Fail("upstream_error", err)
			return &aliResponse, err, responseBody
		}
	}

	var response AliResponse
	err = common.Unmarshal(responseBody, &response)
	if err != nil {
		if managed {
			info.StreamSession.Fail("upstream_json_error", err)
			return &aliResponse, err, responseBody
		}
		common.SysLog("updateTask NewDecoder err: " + err.Error())
		return &aliResponse, err, nil
	}

	return &response, nil, responseBody
}

// aliTaskPollInitialWait / aliTaskPollInterval 是异步任务的首次等待与轮询间隔（测试可缩短）。
var (
	aliTaskPollInitialWait = 5 * time.Second
	aliTaskPollInterval    = 10 * time.Second
)

// asyncTaskWait 按原间隔轮询 taskID，c/info 提供取消与诊断会话；返回最终任务、原始正文及错误。
// 已进入受管图片任务的异常立即交给统一终止流程，旧请求维持既有重试和等待策略。
func asyncTaskWait(c *gin.Context, info *relaycommon.RelayInfo, taskID string) (*AliResponse, []byte, error) {
	waitInterval := aliTaskPollInterval
	step := 0
	maxStep := 20

	var taskResponse AliResponse
	var responseBody []byte

	// 任务已提交，上游按任务生成全部图片并计费：只有我方时限（及请求结束）结束轮询，客户端离开
	// 不中断，取回真实结果按实际张数结算（与主分支一致）。受管图片会话同样如此——轮询已提交的
	// 任务不是流式读取，停下来只会按估算少收。
	requestContext, release := service.RelayUpstreamContext(c)
	defer release()
	select {
	case <-time.After(aliTaskPollInitialWait):
	case <-requestContext.Done():
		return nil, nil, requestContext.Err()
	}

	for {
		logger.LogDebug(c, "asyncTaskWait step %d/%d, wait %s", step, maxStep, waitInterval)
		step++
		rsp, err, body := updateTask(c, info, taskID, requestContext)
		responseBody = body
		if err != nil {
			if info.StreamSession.Active() && info.StreamSession.ExpectedImages > 0 {
				return nil, responseBody, err
			}
			logger.LogWarn(c, "asyncTaskWait UpdateTask err: "+err.Error())
			select {
			case <-time.After(waitInterval):
			case <-requestContext.Done():
				return nil, responseBody, requestContext.Err()
			}
			continue
		}

		if rsp.Output.TaskStatus == "" {
			return &taskResponse, responseBody, nil
		}

		switch rsp.Output.TaskStatus {
		case "FAILED":
			fallthrough
		case "CANCELED":
			fallthrough
		case "SUCCEEDED":
			fallthrough
		case "UNKNOWN":
			return rsp, responseBody, nil
		}
		if step >= maxStep {
			break
		}
		select {
		case <-time.After(waitInterval):
		case <-requestContext.Done():
			return nil, responseBody, requestContext.Err()
		}
	}

	err := fmt.Errorf("aliAsyncTaskWait timeout")
	if info.StreamSession.Active() && info.StreamSession.ExpectedImages > 0 {
		info.StreamSession.Fail("upstream_incomplete", err)
	}
	return nil, nil, err
}

func responseAli2OpenAIImage(c *gin.Context, response *AliResponse, originBody []byte, info *relaycommon.RelayInfo, responseFormat string) *dto.ImageResponse {
	imageResponse := dto.ImageResponse{
		Created: info.StartTime.Unix(),
	}

	if len(response.Output.Results) > 0 {
		imageResponse.Data = response.Output.ResultToOpenAIImageDate(c, responseFormat)
	} else if len(response.Output.Choices) > 0 {
		imageResponse.Data = response.Output.ChoicesToOpenAIImageDate(c, responseFormat)
	}

	imageResponse.Metadata = originBody
	return &imageResponse
}

// aliImageHandler 将 Ali 原生图片响应转换并写出；a 决定同步/任务阶段，c 为下游，resp 为初始响应，info 为结算会话。
// 返回渠道错误与原计费用量；任务响应在读取前声明中间态规则，非受管响应不受影响。
func aliImageHandler(a *Adaptor, c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.Usage) {
	responseFormat := ""
	if imageReq, ok := info.Request.(*dto.ImageRequest); ok {
		responseFormat = imageReq.ResponseFormat
	}
	if !a.IsSyncImageModel {
		relaycommon.UseStreamImageTaskResponse(resp)
	}

	var aliTaskResponse AliResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError), nil
	}
	service.CloseResponseBodyGracefully(resp)
	err = common.Unmarshal(responseBody, &aliTaskResponse)
	if err != nil {
		// 结构观察不替代 DTO 类型检查；原生字段解码失败仍作为上游 JSON 异常保存诊断。
		info.StreamSession.Fail("upstream_json_error", err)
		return types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError), nil
	}

	if aliTaskResponse.Message != "" {
		logger.LogError(c, "ali_async_task_failed: "+aliTaskResponse.Message)
		return types.NewError(errors.New(aliTaskResponse.Message), types.ErrorCodeBadResponse), nil
	}

	var (
		aliResponse    *AliResponse
		originRespBody []byte
	)

	if a.IsSyncImageModel {
		aliResponse = &aliTaskResponse
		originRespBody = responseBody
	} else {
		// 异步图片模型需要轮询任务结果
		aliResponse, originRespBody, err = asyncTaskWait(c, info, aliTaskResponse.Output.TaskId)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponse), nil
		}
		if aliResponse.Output.TaskStatus != "SUCCEEDED" {
			return types.WithOpenAIError(types.OpenAIError{
				Message: aliResponse.Output.Message,
				Type:    "ali_error",
				Param:   "",
				Code:    aliResponse.Output.Code,
			}, resp.StatusCode), nil
		}
	}

	if a.IsSyncImageModel {
		logger.LogDebug(c, "ali_sync_image_result: %s", originRespBody)
	} else {
		logger.LogDebug(c, "ali_async_image_result: %s", originRespBody)
	}

	imageResponses := responseAli2OpenAIImage(c, aliResponse, originRespBody, info, responseFormat)
	count := int64(aliResponse.Usage.ImageCount)
	if count < 0 || count > dto.MaxImageN {
		logger.LogWarn(c, "invalid Ali image usage count %d; falling back to response images or requested quantity", count)
		count = 0
	}
	if count == 0 {
		count = int64(len(imageResponses.Data))
	}
	if count > dto.MaxImageN {
		logger.LogWarn(c, "invalid Ali response image count %d; retaining requested quantity", count)
	} else if count > 0 {
		info.UpdateImageCount(count)
	}
	jsonResponse, err := common.Marshal(imageResponses)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	service.IOCopyBytesGracefully(c, resp, jsonResponse)

	return nil, &dto.Usage{}
}
