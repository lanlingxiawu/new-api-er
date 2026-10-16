package controller

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func isSelfLogExportRoute(c *gin.Context) bool {
	return strings.HasPrefix(c.Request.URL.Path, "/api/log/self/export/jobs")
}

// parseSelfLogExportRequest rejects unknown fields instead of silently accepting
// administrator filters. User scope and columns are assigned only by the server.
func parseSelfLogExportRequest(c *gin.Context) (createLogExportJobRequest, error) {
	var body map[string]any
	var req createLogExportJobRequest
	if err := common.UnmarshalBodyReusable(c, &body); err != nil {
		return req, err
	}
	for key := range body {
		if key != "filters" && key != "options" {
			return req, errors.New("invalid self export field")
		}
	}
	filters, ok := body["filters"].(map[string]any)
	if !ok {
		return req, errors.New("missing self export filters")
	}
	for key := range filters {
		switch key {
		case "start_timestamp", "end_timestamp", "type", "model_name", "token_name", "group":
		default:
			return req, errors.New("invalid self export filter")
		}
	}
	data, err := common.Marshal(filters)
	if err != nil {
		return req, err
	}
	if err = common.Unmarshal(data, &req); err != nil {
		return req, err
	}
	req.Options.Header = true
	req.Options.CSVBOM = true
	if raw, exists := body["options"]; exists {
		options, ok := raw.(map[string]any)
		if !ok {
			return req, errors.New("invalid self export options")
		}
		for key := range options {
			if key != "timezone" && key != "header" && key != "csv_bom" {
				return req, errors.New("invalid self export option")
			}
		}
		data, err = common.Marshal(options)
		if err != nil {
			return req, err
		}
		err = common.Unmarshal(data, &req.Options)
	}
	return req, err
}
