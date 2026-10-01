package common

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// RequestBodyError marks a failure to read or decode the client's request body,
// as opposed to a validation message written for the client. The wrapped error
// is Go's own text (decoder, multipart, file system) and is for logs only;
// PublicRequestError decides what the client sees.
type RequestBodyError struct {
	Err  error
	read bool // reading/storing the body failed, before any decoding
	form bool // the body is a form (multipart or URL-encoded), not JSON
}

func (e *RequestBodyError) Error() string { return e.Err.Error() }
func (e *RequestBodyError) Unwrap() error { return e.Err }

// NewRequestBodyReadError marks err as a failure to read or store the body.
func NewRequestBodyReadError(c *gin.Context, err error) error {
	return &RequestBodyError{Err: err, read: true, form: isFormRequest(c)}
}

// NewRequestFormError marks err as a failure to parse a form body.
func NewRequestFormError(err error) error {
	return &RequestBodyError{Err: err, form: true}
}

// UnmarshalRequestBody is common.UnmarshalBodyReusable with failures marked as
// RequestBodyError, telling a body that could not be read apart from one that
// could not be decoded.
func UnmarshalRequestBody(c *gin.Context, v any) error {
	if _, err := common.GetBodyStorage(c); err != nil {
		return NewRequestBodyReadError(c, err)
	}
	if err := common.UnmarshalBodyReusable(c, v); err != nil {
		return &RequestBodyError{Err: err, form: isFormRequest(c)}
	}
	return nil
}

// ParseMultipartRequestForm is common.ParseMultipartFormReusable with failures
// marked as RequestBodyError.
func ParseMultipartRequestForm(c *gin.Context) (*multipart.Form, error) {
	if _, err := common.GetBodyStorage(c); err != nil {
		return nil, NewRequestBodyReadError(c, err)
	}
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, NewRequestFormError(err)
	}
	return form, nil
}

func isFormRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	contentType := c.Request.Header.Get("Content-Type")
	return strings.Contains(contentType, "multipart/form-data") ||
		strings.Contains(contentType, "application/x-www-form-urlencoded")
}

// PublicRequestError maps a request-body failure to the status and message
// sent to the client, never Go's error text:
//   - too large (including a form over the multipart limits): 413;
//   - the gateway's own storage failed (temp file or disk, while storing the
//     body or while parsing a form into temp files): 500, a server fault;
//   - a JSON value of the wrong type: 400 naming the JSON field and the
//     expected type;
//   - malformed JSON, a malformed form, a body cut short: 400;
//   - reading the body failed otherwise: 500.
//
// ok is false for any other error (validation messages written for clients),
// which callers pass through.
func PublicRequestError(c *gin.Context, err error) (status int, message string, ok bool) {
	if err == nil {
		return 0, "", false
	}
	if IsBodyTooLargeError(err) {
		return http.StatusRequestEntityTooLarge, translate(c, i18n.MsgRelayRequestBodyTooLarge, nil), true
	}
	var bodyErr *RequestBodyError
	isBodyErr := errors.As(err, &bodyErr)
	form := isBodyErr && bodyErr.form
	if isBodyErr && IsLocalIOError(err) {
		return http.StatusInternalServerError, translate(c, i18n.MsgRelayRequestBodyReadFailed, nil), true
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := jsonFieldPath(typeErr.Field)
		if expected := jsonTypeName(typeErr.Type); field != "" && expected != "" {
			return http.StatusBadRequest, translate(c, i18n.MsgRelayRequestFieldInvalid, map[string]any{
				"Field":    field,
				"Expected": expected,
			}), true
		}
		return http.StatusBadRequest, invalidBodyMessage(c, form), true
	}
	var syntaxErr *json.SyntaxError
	var invalidErr *json.InvalidUnmarshalError
	if errors.As(err, &syntaxErr) || errors.As(err, &invalidErr) {
		return http.StatusBadRequest, invalidBodyMessage(c, form), true
	}
	cutShort := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
	if isBodyErr && bodyErr.read && !cutShort {
		return http.StatusInternalServerError, translate(c, i18n.MsgRelayRequestBodyReadFailed, nil), true
	}
	if isBodyErr || cutShort {
		return http.StatusBadRequest, invalidBodyMessage(c, form), true
	}
	return 0, "", false
}

func invalidBodyMessage(c *gin.Context, form bool) string {
	if form {
		return translate(c, i18n.MsgRelayRequestFormInvalid, nil)
	}
	return translate(c, i18n.MsgRelayRequestBodyInvalid, nil)
}

// translate is i18n.T that tolerates a context built outside an HTTP request
// (no Request to read Accept-Language from): it then uses the default language.
func translate(c *gin.Context, key string, args map[string]any) string {
	if c == nil || c.Request == nil {
		return i18n.Translate(i18n.DefaultLang, key, args)
	}
	return i18n.T(c, key, args)
}

// jsonFieldPath removes Go names from the decoder's field path. encoding/json
// includes embedded struct names in it (the dto UnmarshalJSON helpers embed an
// "Alias" type, giving "Alias.generationConfig.Alias.maxOutputTokens"). JSON
// keys of the relay APIs start lower-case, so a segment starting with an
// upper-case letter is such a Go name and is dropped.
func jsonFieldPath(field string) string {
	if field == "" {
		return ""
	}
	segments := strings.Split(field, ".")
	kept := segments[:0]
	for _, segment := range segments {
		if segment == "" || ('A' <= segment[0] && segment[0] <= 'Z') {
			continue
		}
		kept = append(kept, segment)
	}
	return strings.Join(kept, ".")
}

var jsonNumberType = reflect.TypeOf(json.Number(""))

// jsonTypeName names the JSON value a Go type decodes from, in JSON Schema
// terms, or "" when there is no single answer.
func jsonTypeName(t reflect.Type) string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return ""
	}
	if t == jsonNumberType {
		return "number"
	}
	switch t.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return "integer >= 0"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Struct, reflect.Map:
		return "object"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "string" // []byte decodes from a base64 string
		}
		return "array"
	case reflect.Array:
		return "array"
	}
	return ""
}

// newRequestBodyTaskError is the task-submit counterpart of the relay's
// invalid-request error: status and message from PublicRequestError, the raw
// error only in the log.
func newRequestBodyTaskError(c *gin.Context, err error, code string) *dto.TaskError {
	status, message, ok := PublicRequestError(c, err)
	if !ok {
		status, message = http.StatusBadRequest, invalidBodyMessage(c, isFormRequest(c))
	}
	if status >= http.StatusInternalServerError {
		logger.LogError(c, "task request body read failed: "+common.LocalLogPreview(err.Error()))
	} else {
		logger.LogWarn(c, "task request body parse failed: "+common.LocalLogPreview(err.Error()))
	}
	taskErr := createTaskError(errors.New(message), code, status, true)
	taskErr.SkipRetry = true
	return taskErr
}

// IsBodyTooLargeError reports a body over the request size limit, or a form
// over the multipart memory/file limits.
func IsBodyTooLargeError(err error) bool {
	return common.IsRequestBodyTooLargeError(err) || errors.Is(err, multipart.ErrMessageTooLarge)
}

// IsLocalIOError reports a failure of the gateway's own file system: creating
// or writing a temp file, reading a disk-backed body. These are server faults,
// unlike malformed input (multipart/textproto/mime syntax errors) or a client
// connection that broke, which carry no *fs.PathError.
func IsLocalIOError(err error) bool {
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return false // a network connection (client or upstream), not our disk
	}
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	var syscallErr *os.SyscallError
	return errors.As(err, &pathErr) || errors.As(err, &linkErr) || errors.As(err, &syscallErr)
}

// ErrorCodeImageRequestUnsupported: the selected channel's adaptor cannot
// express this image request (not implemented for the channel type, or the
// model/mode is not supported by it). Another channel may be able to.
const ErrorCodeImageRequestUnsupported types.ErrorCode = "image_request_unsupported"

// IsLocalRequestBuildError reports an error raised while the gateway built the
// upstream request, before the channel's upstream was called for it. It says
// nothing about the channel's health, so it must not auto-disable the channel
// even when its status is in AutomaticDisableStatusCodes.
func IsLocalRequestBuildError(err *types.NewAPIError) bool {
	code := err.GetErrorCode()
	return code == types.ErrorCodeConvertRequestFailed || code == ErrorCodeImageRequestUnsupported
}
