package dto

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
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
		err := common.Unmarshal(e.Error, &openAIError)
		if err == nil && openAIError.Message != "" {
			openAIError.Message = common.StripRequestIds(openAIError.Message)
			return &openAIError
		}
	}
	return nil
}

func (e GeneralErrorResponse) ToMessage() string {
	if len(e.Error) > 0 {
		switch common.GetJsonType(e.Error) {
		case "object":
			var openAIError types.OpenAIError
			err := common.Unmarshal(e.Error, &openAIError)
			if err == nil && openAIError.Message != "" {
				return common.StripRequestIds(openAIError.Message)
			}
		case "string":
			var msg string
			err := common.Unmarshal(e.Error, &msg)
			if err == nil && msg != "" {
				return common.StripRequestIds(msg)
			}
		default:
			return common.StripRequestIds(string(e.Error))
		}
	}
	if e.Message != "" {
		return common.StripRequestIds(e.Message)
	}
	if e.Msg != "" {
		return common.StripRequestIds(e.Msg)
	}
	if e.Err != "" {
		return common.StripRequestIds(e.Err)
	}
	if e.ErrorMsg != "" {
		return common.StripRequestIds(e.ErrorMsg)
	}
	if e.Detail != "" {
		return common.StripRequestIds(e.Detail)
	}
	if e.Header.Message != "" {
		return common.StripRequestIds(e.Header.Message)
	}
	if e.Response.Error.Message != "" {
		return common.StripRequestIds(e.Response.Error.Message)
	}
	return ""
}
