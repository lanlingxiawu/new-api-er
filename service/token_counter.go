package service

import (
	"errors"
	"fmt"
	"image"
	"math"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	constant2 "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func getImageToken(c *gin.Context, fileMeta *types.FileMeta, model string, stream bool) (int, error) {
	if fileMeta == nil || fileMeta.Source == nil {
		return 0, fmt.Errorf("image_url_is_nil")
	}

	// Model classification
	lowerModel := strings.ToLower(model)

	// Special cases from existing behavior
	if strings.HasPrefix(lowerModel, "glm-4") {
		return glm4ImageTokens, nil
	}

	baseTokens, tileTokens, isPatchBased, multiplier := imageTokenModelParams(lowerModel)

	// Respect existing feature flags/short-circuits
	if fileMeta.Detail == "low" && !isPatchBased {
		return baseTokens, nil
	}

	// Whether to count image tokens at all
	if !constant.GetMediaToken {
		return 3 * baseTokens, nil
	}

	if !constant.GetMediaTokenNotStream && !stream {
		return 3 * baseTokens, nil
	}
	// Normalize detail
	if fileMeta.Detail == "auto" || fileMeta.Detail == "" {
		fileMeta.Detail = "high"
	}

	// 使用统一的文件服务获取图片配置
	config, format, err := GetImageConfig(c, fileMeta.Source)
	if err != nil {
		return 0, err
	}
	return imageTokensFromConfig(c, fileMeta, config, format, baseTokens, tileTokens, isPatchBased, multiplier)
}

// glm4ImageTokens is the flat per-image estimate for glm-4 models.
const glm4ImageTokens = 1047

// imageTokenFallback is the fixed per-image estimate used when the image cannot
// be inspected (download refused / failed, or undecodable data). It equals what
// getImageToken returns when local media counting is disabled. The request is
// still forwarded: the upstream fetches the image itself, and settlement uses
// the upstream usage, so only the pre-consume estimate is affected (and, when
// the upstream reports no usage, the settled prompt tokens).
func imageTokenFallback(model string, detail string) int {
	lowerModel := strings.ToLower(model)
	if strings.HasPrefix(lowerModel, "glm-4") {
		return glm4ImageTokens
	}
	baseTokens, _, isPatchBased, _ := imageTokenModelParams(lowerModel)
	if detail == "low" && !isPatchBased {
		return baseTokens
	}
	return 3 * baseTokens
}

// imageTokenModelParams classifies a (lower-cased) OpenAI model for image
// token estimation: tile-based models get base/tile tokens, patch-based models
// (32x32 patches, capped at 1536) get a multiplier.
func imageTokenModelParams(lowerModel string) (baseTokens, tileTokens int, isPatchBased bool, multiplier float64) {
	// Defaults for 4o/4.1/4.5 family unless overridden below
	baseTokens = 85
	tileTokens = 170

	// Patch-based models (32x32 patches, capped at 1536, with multiplier)
	multiplier = 1.0
	switch {
	case strings.Contains(lowerModel, "gpt-4.1-mini"):
		isPatchBased = true
		multiplier = 1.62
	case strings.Contains(lowerModel, "gpt-4.1-nano"):
		isPatchBased = true
		multiplier = 2.46
	case strings.HasPrefix(lowerModel, "o4-mini"):
		isPatchBased = true
		multiplier = 1.72
	case strings.HasPrefix(lowerModel, "gpt-5-mini"):
		isPatchBased = true
		multiplier = 1.62
	case strings.HasPrefix(lowerModel, "gpt-5-nano"):
		isPatchBased = true
		multiplier = 2.46
	}

	// Tile-based model tokens and bases per doc
	if !isPatchBased {
		if strings.HasPrefix(lowerModel, "gpt-4o-mini") {
			baseTokens = 2833
			tileTokens = 5667
		} else if strings.HasPrefix(lowerModel, "gpt-5-chat-latest") || (strings.HasPrefix(lowerModel, "gpt-5") && !strings.Contains(lowerModel, "mini") && !strings.Contains(lowerModel, "nano")) {
			baseTokens = 70
			tileTokens = 140
		} else if strings.HasPrefix(lowerModel, "o1") || strings.HasPrefix(lowerModel, "o3") || strings.HasPrefix(lowerModel, "o1-pro") {
			baseTokens = 75
			tileTokens = 150
		} else if strings.Contains(lowerModel, "computer-use-preview") {
			baseTokens = 65
			tileTokens = 129
		} else if strings.Contains(lowerModel, "4.1") || strings.Contains(lowerModel, "4o") || strings.Contains(lowerModel, "4.5") {
			baseTokens = 85
			tileTokens = 170
		}
	}
	return baseTokens, tileTokens, isPatchBased, multiplier
}

func imageTokensFromConfig(c *gin.Context, fileMeta *types.FileMeta, config image.Config, format string, baseTokens, tileTokens int, isPatchBased bool, multiplier float64) (int, error) {
	if config.Width == 0 || config.Height == 0 {
		// not an image, but might be a valid file
		if format != "" {
			// file type
			return 3 * baseTokens, nil
		}
		return 0, errors.New(fmt.Sprintf("fail to decode image config: %s", fileMeta.GetIdentifier()))
	}

	width := config.Width
	height := config.Height
	logger.LogDebug(c, "image token input: format=%s, width=%d, height=%d", format, width, height)

	if isPatchBased {
		// 32x32 patch-based calculation with 1536 cap and model multiplier
		ceilDiv := func(a, b int) int { return (a + b - 1) / b }
		rawPatchesW := ceilDiv(width, 32)
		rawPatchesH := ceilDiv(height, 32)
		rawPatches := rawPatchesW * rawPatchesH
		if rawPatches > 1536 {
			// scale down
			area := float64(width * height)
			r := math.Sqrt(float64(32*32*1536) / area)
			wScaled := float64(width) * r
			hScaled := float64(height) * r
			// adjust to fit whole number of patches after scaling
			adjW := math.Floor(wScaled/32.0) / (wScaled / 32.0)
			adjH := math.Floor(hScaled/32.0) / (hScaled / 32.0)
			adj := math.Min(adjW, adjH)
			if !math.IsNaN(adj) && adj > 0 {
				r = r * adj
			}
			wScaled = float64(width) * r
			hScaled = float64(height) * r
			patchesW := math.Ceil(wScaled / 32.0)
			patchesH := math.Ceil(hScaled / 32.0)
			imageTokens := int(patchesW * patchesH)
			if imageTokens > 1536 {
				imageTokens = 1536
			}
			return int(math.Round(float64(imageTokens) * multiplier)), nil
		}
		// below cap
		imageTokens := rawPatches
		return int(math.Round(float64(imageTokens) * multiplier)), nil
	}

	// Tile-based calculation for 4o/4.1/4.5/o1/o3/etc.
	// Step 1: fit within 2048x2048 square
	maxSide := math.Max(float64(width), float64(height))
	fitScale := 1.0
	if maxSide > 2048 {
		fitScale = maxSide / 2048.0
	}
	fitW := int(math.Round(float64(width) / fitScale))
	fitH := int(math.Round(float64(height) / fitScale))

	// Step 2: scale so that shortest side is exactly 768
	minSide := math.Min(float64(fitW), float64(fitH))
	if minSide == 0 {
		return baseTokens, nil
	}
	shortScale := 768.0 / minSide
	finalW := int(math.Round(float64(fitW) * shortScale))
	finalH := int(math.Round(float64(fitH) * shortScale))

	// Count 512px tiles
	tilesW := (finalW + 512 - 1) / 512
	tilesH := (finalH + 512 - 1) / 512
	tiles := tilesW * tilesH

	logger.LogDebug(c, "image token scaled size: width=%d, height=%d, tiles=%d", finalW, finalH, tiles)

	return tiles*tileTokens + baseTokens, nil
}

func EstimateRequestToken(c *gin.Context, meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error) {
	// 是否统计token
	if !constant.CountToken {
		return 0, nil
	}

	if meta == nil {
		return 0, errors.New("token count meta is nil")
	}

	if info.RelayFormat == types.RelayFormatOpenAIRealtime {
		return 0, nil
	}
	if info.RelayMode == constant2.RelayModeAudioTranscription || info.RelayMode == constant2.RelayModeAudioTranslation {
		multiForm, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return 0, fmt.Errorf("error parsing multipart form: %v", err)
		}
		fileHeaders := multiForm.File["file"]
		totalAudioToken := 0
		for _, fileHeader := range fileHeaders {
			file, err := fileHeader.Open()
			if err != nil {
				return 0, fmt.Errorf("error opening audio file: %v", err)
			}
			defer file.Close()
			// get ext and io.seeker
			ext := filepath.Ext(fileHeader.Filename)
			duration, err := common.GetAudioDuration(c.Request.Context(), file, ext)
			if err != nil {
				return 0, fmt.Errorf("error getting audio duration: %v", err)
			}
			// duration 来自用户上传文件的元数据，可被伪造成天文数字或负数。
			// 负值会让 token 估算变成负数（低估预扣费），先钳到 0 再转换。
			if duration < 0 {
				duration = 0
			}
			// 一分钟 1000 token，与 $price / minute 对齐。
			totalAudioToken += common.QuotaRound(math.Ceil(duration) / 60.0 * 1000)
		}
		return totalAudioToken, nil
	}

	model := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	tkm := 0

	if meta.TokenType == types.TokenTypeTextNumber {
		tkm += utf8.RuneCountInString(meta.CombineText)
	} else {
		tkm += CountTextToken(meta.CombineText, model)
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		tkm += meta.ToolsCount * 8
		tkm += meta.MessagesCount * 3 // 每条消息的格式化token数量
		tkm += meta.NameCount * 3
		tkm += 3
	}

	shouldFetchFiles := true

	if info.RelayFormat == types.RelayFormatGemini {
		shouldFetchFiles = false
	}

	// 是否本地计算媒体token数量
	if !constant.GetMediaToken {
		shouldFetchFiles = false
	}

	// 是否在非流模式下本地计算媒体token数量
	if !constant.GetMediaTokenNotStream && !info.IsStream {
		shouldFetchFiles = false
	}

	// 使用统一的文件服务获取文件类型。
	// 媒体取不到（下载被拒 / 失败 / 超限）或解不出图片尺寸时只用默认估算，不拒绝请求：
	// 上游能自己取到的图片不该因为网关取不到而 500，结算又以上游 usage 为准。
	// 取失败的文件记下来，后面不再为它二次下载。
	unreadable := make([]bool, len(meta.Files))
	for i, file := range meta.Files {
		if file.Source == nil {
			continue
		}

		// 如果文件类型未知且需要获取，通过 MIME 类型检测
		if file.FileType == "" || (file.Source.IsURL() && shouldFetchFiles) {
			// 注意：这里我们直接调用 LoadFileSource 而不是 GetMimeType
			// 因为 GetMimeType 内部可能会调用 GetFileTypeFromUrl (HEAD 请求)
			// 而我们这里既然要计算 token，通常需要完整数据
			cachedData, err := LoadFileSource(c, file.Source, "token_counter")
			if err != nil {
				unreadable[i] = true
				if shouldFetchFiles {
					logger.LogWarn(c, fmt.Sprintf("count token: media index[%d] (%s) unreadable, using default estimate: %s",
						i, describeFileSourceForLog(file.Source), redactFileSourceError(file.Source, err)))
				}
				continue
			}
			file.FileType = DetectFileType(cachedData.MimeType)
		}
	}

	for i, file := range meta.Files {
		switch file.FileType {
		case types.FileTypeImage:
			if common.IsOpenAITextModel(model) {
				if unreadable[i] {
					tkm += imageTokenFallback(model, file.Detail)
					continue
				}
				token, err := getImageToken(c, file, model, info.IsStream)
				if err != nil {
					logger.LogWarn(c, fmt.Sprintf("count token: image index[%d] (%s) not measurable, using default estimate: %s",
						i, describeFileSourceForLog(file.Source), redactFileSourceError(file.Source, err)))
					token = imageTokenFallback(model, file.Detail)
				}
				tkm += token
			} else {
				tkm += 520
			}
		case types.FileTypeAudio:
			tkm += 256
		case types.FileTypeVideo:
			tkm += 4096 * 2
		case types.FileTypeFile:
			tkm += 4096
		default:
			tkm += 4096 // Default case for unknown file types
		}
	}

	common.SetContextKey(c, constant.ContextKeyPromptTokens, tkm)
	return tkm, nil
}

// describeFileSourceForLog names a media source without its full URL or data:
// URLs can carry signed credentials in the query string.
func describeFileSourceForLog(source types.FileSource) string {
	if u, ok := source.(*types.URLSource); ok {
		if parsed, err := url.Parse(u.URL); err == nil && parsed.Host != "" {
			return "url host=" + parsed.Host
		}
		return "url"
	}
	if source == nil {
		return "unknown"
	}
	return "base64"
}

// urlInTextPattern matches anything URL-shaped inside an error message, up to
// the next whitespace. Download errors quote the URL in re-serialized forms
// (userinfo masked, path percent-encoded, redirect targets), so matching the
// original string is not enough; and quotes / angle brackets may stay
// unescaped in the query, so they must not end the match.
var urlInTextPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.\-]*://\S+`)

// redactFileSourceError removes every URL (download errors embed them) from
// err before it is logged.
func redactFileSourceError(source types.FileSource, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if u, ok := source.(*types.URLSource); ok && u.URL != "" {
		msg = strings.ReplaceAll(msg, u.URL, "<url>")
	}
	return urlInTextPattern.ReplaceAllString(msg, "<url>")
}

func CountTokenRealtime(info *relaycommon.RelayInfo, request dto.RealtimeEvent, model string) (int, int, error) {
	audioToken := 0
	textToken := 0
	switch request.Type {
	case dto.RealtimeEventTypeSessionUpdate:
		if request.Session != nil {
			msgTokens := CountTextToken(request.Session.Instructions, model)
			textToken += msgTokens
		}
	case dto.RealtimeEventResponseAudioDelta:
		// count audio token
		atk, err := CountAudioTokenOutput(request.Delta, info.OutputAudioFormat)
		if err != nil {
			return 0, 0, fmt.Errorf("error counting audio token: %v", err)
		}
		audioToken += atk
	case dto.RealtimeEventResponseAudioTranscriptionDelta, dto.RealtimeEventResponseFunctionCallArgumentsDelta:
		// count text token
		tkm := CountTextToken(request.Delta, model)
		textToken += tkm
	case dto.RealtimeEventInputAudioBufferAppend:
		// count audio token
		atk, err := CountAudioTokenInput(request.Audio, info.InputAudioFormat)
		if err != nil {
			return 0, 0, fmt.Errorf("error counting audio token: %v", err)
		}
		audioToken += atk
	case dto.RealtimeEventConversationItemCreated:
		if request.Item != nil {
			switch request.Item.Type {
			case "message":
				for _, content := range request.Item.Content {
					if content.Type == "input_text" {
						tokens := CountTextToken(content.Text, model)
						textToken += tokens
					}
				}
			}
		}
	case dto.RealtimeEventTypeResponseDone:
		// count tools token
		if !info.IsFirstRequest {
			if info.RealtimeTools != nil && len(info.RealtimeTools) > 0 {
				for _, tool := range info.RealtimeTools {
					toolTokens := CountTokenInput(tool, model)
					textToken += 8
					textToken += toolTokens
				}
			}
		}
	}
	return textToken, audioToken, nil
}

func CountTokenInput(input any, model string) int {
	switch v := input.(type) {
	case string:
		return CountTextToken(v, model)
	case []string:
		text := ""
		for _, s := range v {
			text += s
		}
		return CountTextToken(text, model)
	case []interface{}:
		text := ""
		for _, item := range v {
			text += fmt.Sprintf("%v", item)
		}
		return CountTextToken(text, model)
	}
	return CountTokenInput(fmt.Sprintf("%v", input), model)
}

func CountAudioTokenInput(audioBase64 string, audioFormat string) (int, error) {
	if audioBase64 == "" {
		return 0, nil
	}
	duration, err := parseAudio(audioBase64, audioFormat)
	if err != nil {
		return 0, err
	}
	// duration 来自用户提供的音频元数据，饱和转换防止 int 回绕
	return common.QuotaFromFloat(duration / 60 * 100 / 0.06), nil
}

func CountAudioTokenOutput(audioBase64 string, audioFormat string) (int, error) {
	if audioBase64 == "" {
		return 0, nil
	}
	duration, err := parseAudio(audioBase64, audioFormat)
	if err != nil {
		return 0, err
	}
	// duration 来自上游返回的音频元数据，饱和转换防止 int 回绕
	return common.QuotaFromFloat(duration / 60 * 200 / 0.24), nil
}

// CountTextToken 统计文本的token数量，仅OpenAI模型使用tokenizer，其余模型使用估算
func CountTextToken(text string, model string) int {
	if text == "" {
		return 0
	}
	if common.IsOpenAITextModel(model) {
		tokenEncoder := getTokenEncoder(model)
		return getTokenNum(tokenEncoder, text)
	} else {
		// 非openai模型，使用tiktoken-go计算没有意义，使用估算节省资源
		return EstimateTokenByModel(model, text)
	}
}
