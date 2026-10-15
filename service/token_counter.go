package service

import (
	"errors"
	"fmt"
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
	"github.com/QuantumNous/new-api/tokenkit"

	"github.com/gin-gonic/gin"
)

func getImageToken(c *gin.Context, fileMeta *types.FileMeta, model string, stream bool) (int, error) {
	if fileMeta == nil || fileMeta.Source == nil {
		return 0, fmt.Errorf("image_url_is_nil")
	}

	img := tokenkit.Image{Detail: fileMeta.Detail}
	// Reading the size may download a URL image; operators can turn that off,
	// and the estimate then assumes a typical image size.
	measure := constant.GetMediaToken && (stream || constant.GetMediaTokenNotStream)
	if !measure || !tokenkit.ImageNeedsSize(model, img.Detail) {
		return tokenkit.ImageTokens(model, img), nil
	}

	config, format, err := GetImageConfig(c, fileMeta.Source)
	if err != nil {
		return 0, err
	}
	if (config.Width == 0 || config.Height == 0) && format == "" {
		return 0, fmt.Errorf("fail to decode image config: %s", fileMeta.GetIdentifier())
	}
	img.Width, img.Height = config.Width, config.Height
	logger.LogDebug(c, "image token input: format=%s, width=%d, height=%d", format, img.Width, img.Height)
	return tokenkit.ImageTokens(model, img), nil
}

func EstimateRequestToken(c *gin.Context, meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error) {
	// 是否统计token
	if !constant.CountToken {
		return 0, nil
	}
	return CountRequestToken(c, meta, info)
}

// CountRequestToken counts request tokens regardless of the billing estimation
// switch. Utility endpoints such as Claude's messages/count_tokens must remain
// available even when operators disable request-token estimation for relays.
func CountRequestToken(c *gin.Context, meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error) {
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
		tkm += tokenkit.Count(model, meta.CombineText)
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
			if unreadable[i] {
				tkm += tokenkit.ImageTokens(model, tokenkit.Image{Detail: file.Detail})
				continue
			}
			token, err := getImageToken(c, file, model, info.IsStream)
			if err != nil {
				logger.LogWarn(c, fmt.Sprintf("count token: image index[%d] (%s) not measurable, using default estimate: %s", i, describeFileSourceForLog(file.Source), redactFileSourceError(file.Source, err)))
				token = tokenkit.ImageTokens(model, tokenkit.Image{Detail: file.Detail})
			}
			tkm += token
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
			msgTokens := tokenkit.Count(model, request.Session.Instructions)
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
		tkm := tokenkit.Count(model, request.Delta)
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
						tokens := tokenkit.Count(model, content.Text)
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
		return tokenkit.Count(model, v)
	case []string:
		var text strings.Builder
		for _, s := range v {
			text.WriteString(s)
		}
		return tokenkit.Count(model, text.String())
	case []any:
		var text strings.Builder
		for _, item := range v {
			text.WriteString(fmt.Sprintf("%v", item))
		}
		return tokenkit.Count(model, text.String())
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
