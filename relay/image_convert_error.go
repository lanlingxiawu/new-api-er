package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

type imageConvertFailure int

const (
	// imageConvertServerFault: building the upstream request failed on the
	// gateway's side (temp files, form writing) or the channel is misconfigured.
	imageConvertServerFault imageConvertFailure = iota
	// imageConvertUpstreamFault: a call the adaptor makes while converting
	// (Replicate's file upload) failed.
	imageConvertUpstreamFault
	// imageConvertUnsupported: this channel's adaptor cannot do the request.
	imageConvertUnsupported
	// imageConvertBadRequest: the request content is missing or invalid.
	imageConvertBadRequest
	// imageConvertTooLarge: the uploaded form exceeds the size limits.
	imageConvertTooLarge
)

// Adaptor ConvertImageRequest errors are plain errors, so the kind is read from
// where the error comes from: its Go type (file system, network, JSON), the
// adaptor that produced it, and the adaptors' own message text below. Every
// entry is a message an adaptor actually returns; an error matching none is
// treated as a server fault, which stays retryable.
var (
	// Adaptors that cannot do image requests, or not for this model/mode.
	imageConvertUnsupportedMessages = []string{
		"endpoint not supported",                   // codex, submodel
		"only imagen models are supported",         // gemini
		"unsupported image relay mode",             // ali, minimax
		"does not support image requests",          // advancedcustom converter
		"does not support request path",            // advancedcustom route
		"not supported model for image generation", // gemini
	}
	// Multipart form parsing inside an adaptor; the wrapped error decides
	// between client syntax (400), size (413) and the gateway's disk (500).
	imageConvertFormParseMessages = []string{
		"failed to parse multipart form",          // openai edits
		"parse multipart form failed",             // replicate
		"failed to parse image edit form request", // ali
	}
	// Request content the client must fix.
	imageConvertBadRequestMessages = []string{
		"image is required",                       // openai, ali
		"prompt is required",                      // replicate
		"image file is required for edits",        // replicate
		"no multipart form data found",            // openai edits
		"parameters.n must be an integer between", // ali
	}
	// Replicate uploads the input image to its file API while converting.
	replicateUploadUpstreamMessages = []string{
		"replicate adaptor: upload image failed",
		"replicate adaptor: read upload response failed",
		"replicate adaptor: decode upload response failed",
		"replicate adaptor: upload response missing url",
	}
)

func containsAny(s string, fragments []string) bool {
	for _, fragment := range fragments {
		if strings.Contains(s, fragment) {
			return true
		}
	}
	return false
}

func classifyImageConvertError(info *relaycommon.RelayInfo, err error) imageConvertFailure {
	message := strings.ToLower(err.Error())
	apiType := -1
	if info != nil && info.ChannelMeta != nil {
		apiType = info.ApiType
	}
	switch {
	case relaycommon.IsBodyTooLargeError(err):
		return imageConvertTooLarge
	case relaycommon.IsLocalIOError(err):
		return imageConvertServerFault
	case apiType == constant.APITypeReplicate && containsAny(message, replicateUploadUpstreamMessages):
		return imageConvertUpstreamFault
	case containsAny(message, imageConvertFormParseMessages):
		// Local disk and size were handled above: what is left is the form itself.
		return imageConvertBadRequest
	case isNetworkError(err):
		return imageConvertUpstreamFault
	case message == "not implemented" || containsAny(message, imageConvertUnsupportedMessages):
		return imageConvertUnsupported
	case apiType == constant.APITypeAdvancedCustom:
		// Every other advanced-custom conversion error is its channel
		// configuration (advanced_custom is required, …incoming_path is required).
		return imageConvertServerFault
	case containsAny(message, imageConvertBadRequestMessages), isJSONDecodeError(err):
		// JSON errors here come from client-supplied extra/parameters/input fields.
		return imageConvertBadRequest
	}
	return imageConvertServerFault
}

func isNetworkError(err error) bool {
	var urlErr *url.Error
	var netErr net.Error
	return errors.As(err, &urlErr) || errors.As(err, &netErr)
}

func isJSONDecodeError(err error) bool {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	return errors.As(err, &typeErr) || errors.As(err, &syntaxErr)
}

// newImageConvertError builds the relay error for a failed
// adaptor.ConvertImageRequest. The client gets a translated message, never the
// adaptor's text (Go errors, file paths); the raw error is logged.
//   - unsupported by this channel: 501, retryable so the next channel is tried;
//   - invalid request content: 400 (413 when too large), not retried;
//   - server-side failure or channel misconfiguration: 500, retryable;
//   - the adaptor's own upstream call failed: 502, retryable.
//
// The retryable ones use codes that relaycommon.IsLocalRequestBuildError
// recognises, so they never auto-disable the channel.
func newImageConvertError(c *gin.Context, info *relaycommon.RelayInfo, err error) *types.NewAPIError {
	channelId := 0
	model := ""
	if info != nil {
		model = info.OriginModelName
		if info.ChannelMeta != nil {
			channelId = info.ChannelId
		}
	}
	raw := common.LocalLogPreview(err.Error())
	switch classifyImageConvertError(info, err) {
	case imageConvertUnsupported:
		logger.LogWarn(c, fmt.Sprintf("channel #%d cannot convert the image request: %s", channelId, raw))
		return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayImageRequestUnsupported, map[string]any{"Model": model})),
			relaycommon.ErrorCodeImageRequestUnsupported, http.StatusNotImplemented)
	case imageConvertBadRequest:
		logger.LogWarn(c, fmt.Sprintf("channel #%d rejected the image request content: %s", channelId, raw))
		return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayImageRequestInvalid)),
			types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	case imageConvertTooLarge:
		logger.LogWarn(c, fmt.Sprintf("channel #%d: image request too large: %s", channelId, raw))
		return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayRequestBodyTooLarge)),
			types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
	case imageConvertUpstreamFault:
		logger.LogError(c, fmt.Sprintf("channel #%d: upstream call while building the image request failed: %s", channelId, raw))
		return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayRequestPrepareFailed)),
			types.ErrorCodeConvertRequestFailed, http.StatusBadGateway)
	default:
		logger.LogError(c, fmt.Sprintf("channel #%d failed to build the image request: %s", channelId, raw))
		return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayRequestPrepareFailed)),
			types.ErrorCodeConvertRequestFailed, http.StatusInternalServerError)
	}
}
