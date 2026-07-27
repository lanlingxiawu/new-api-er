package dto

import (
	"encoding/json"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
)

//type OpenAIError struct {
//	Message string `json:"message"`
//	Type    string `json:"type"`
//	Param   string `json:"param"`
//	Code    any    `json:"code"`
//}

type OpenAIErrorWithStatusCode struct {
	Error      types.OpenAIError `json:"error"`
	StatusCode int               `json:"status_code"`
	LocalError bool
}

type GeneralErrorResponse struct {
	Error    json.RawMessage `json:"error"`
	Message  string          `json:"message"`
	Msg      string          `json:"msg"`
	Err      string          `json:"err"`
	ErrorMsg string          `json:"error_msg"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Detail   string          `json:"detail,omitempty"`
	Header   struct {
		Message string `json:"message"`
	} `json:"header"`
	Response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

func (e GeneralErrorResponse) TryToOpenAIError() *types.OpenAIError {
	var openAIError types.OpenAIError
	if len(e.Error) > 0 {
		err := kitutil.Unmarshal(e.Error, &openAIError)
		if err == nil && openAIError.Message != "" {
			openAIError.Message = kitutil.StripRequestIds(openAIError.Message)
			return &openAIError
		}
	}
	return nil
}

func (e GeneralErrorResponse) ToMessage() string {
	if len(e.Error) > 0 {
		switch kitutil.GetJsonType(e.Error) {
		case "object":
			var openAIError types.OpenAIError
			err := kitutil.Unmarshal(e.Error, &openAIError)
			if err == nil && openAIError.Message != "" {
				return kitutil.StripRequestIds(openAIError.Message)
			}
		case "string":
			var msg string
			err := kitutil.Unmarshal(e.Error, &msg)
			if err == nil && msg != "" {
				return kitutil.StripRequestIds(msg)
			}
		default:
			return kitutil.StripRequestIds(string(e.Error))
		}
	}
	if e.Message != "" {
		return kitutil.StripRequestIds(e.Message)
	}
	if e.Msg != "" {
		return kitutil.StripRequestIds(e.Msg)
	}
	if e.Err != "" {
		return kitutil.StripRequestIds(e.Err)
	}
	if e.ErrorMsg != "" {
		return kitutil.StripRequestIds(e.ErrorMsg)
	}
	if e.Detail != "" {
		return kitutil.StripRequestIds(e.Detail)
	}
	if e.Header.Message != "" {
		return kitutil.StripRequestIds(e.Header.Message)
	}
	if e.Response.Error.Message != "" {
		return kitutil.StripRequestIds(e.Response.Error.Message)
	}
	return ""
}
