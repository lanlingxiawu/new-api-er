package controller

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// QuotaExportScope binds the quota_data export to a dashboard session.
// Personal access tokens cannot create or download these files.
func QuotaExportScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.GetSessionAuthIdentity(c); !ok || c.GetInt("role") < common.RoleAdminUser {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgForbidden)})
			return
		}
		c.Set("log_export_access", model.LogExportAccess{UserID: c.GetInt("id"), Role: c.GetInt("role"), Scope: "quota_admin"})
		c.Next()
	}
}

func parseQuotaExportRequest(c *gin.Context, role int) (*model.QuotaExportFilter, int64, int64, model.LogExportOptions, error) {
	invalid := errors.New("invalid quota export request")
	opts := model.LogExportOptions{Header: true, CSVBOM: true}
	var body map[string]json.RawMessage
	if err := common.UnmarshalBodyReusable(c, &body); err != nil {
		return nil, 0, 0, opts, err
	}
	for key := range body {
		if key != "filters" && key != "options" {
			return nil, 0, 0, opts, invalid
		}
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(body["filters"], &fields); err != nil || fields == nil {
		return nil, 0, 0, opts, invalid
	}
	allowed := []string{"start_timestamp", "end_timestamp", "user_id", "username", "model_name", "use_group", "channel_id"}
	if role >= common.RoleRootUser {
		allowed = append(allowed, "token_id", "node_name")
	}
	for key, value := range fields {
		if string(value) == "null" || !slices.Contains(allowed, key) {
			return nil, 0, 0, opts, invalid
		}
	}
	start, err := quotaExportInt(fields["start_timestamp"], true)
	if err != nil {
		return nil, 0, 0, opts, invalid
	}
	end, err := quotaExportInt(fields["end_timestamp"], true)
	if err != nil {
		return nil, 0, 0, opts, invalid
	}
	filter := &model.QuotaExportFilter{}
	if raw, ok := fields["user_id"]; ok {
		value, err := quotaExportInt(raw, false)
		if err != nil {
			return nil, 0, 0, opts, invalid
		}
		id := int(value)
		filter.UserID = &id
	}
	if raw, ok := fields["channel_id"]; ok {
		value, err := quotaExportInt(raw, false)
		if err != nil {
			return nil, 0, 0, opts, invalid
		}
		id := int(value)
		filter.ChannelID = &id
	}
	if raw, ok := fields["token_id"]; ok {
		value, err := quotaExportInt(raw, false)
		if err != nil {
			return nil, 0, 0, opts, invalid
		}
		id := int(value)
		filter.TokenID = &id
	}
	for _, item := range []struct {
		key string
		dst *string
	}{
		{"username", &filter.Username},
		{"model_name", &filter.ModelName},
		{"use_group", &filter.UseGroup},
		{"node_name", &filter.NodeName},
	} {
		raw, ok := fields[item.key]
		if !ok {
			continue
		}
		var text string
		if err := common.Unmarshal(raw, &text); err != nil {
			return nil, 0, 0, opts, invalid
		}
		text = strings.TrimSpace(text)
		if text != "" && utf8.RuneCountInString(text) > 64 {
			return nil, 0, 0, opts, invalid
		}
		*item.dst = text
	}
	if raw, ok := body["options"]; ok {
		var options map[string]json.RawMessage
		if common.Unmarshal(raw, &options) != nil || options == nil {
			return nil, 0, 0, opts, invalid
		}
		for key, value := range options {
			if string(value) == "null" || !slices.Contains([]string{"header", "csv_bom", "timezone"}, key) {
				return nil, 0, 0, opts, invalid
			}
		}
		if err := common.Unmarshal(raw, &opts); err != nil {
			return nil, 0, 0, opts, invalid
		}
	}
	if opts.Timezone != "" {
		if _, err := time.LoadLocation(opts.Timezone); err != nil {
			return nil, 0, 0, opts, invalid
		}
	}
	return filter, start, end, opts, nil
}

func quotaExportInt(raw json.RawMessage, required bool) (int64, error) {
	if len(raw) == 0 {
		if required {
			return 0, errors.New("missing")
		}
		return 0, errors.New("missing")
	}
	var num json.Number
	if err := common.Unmarshal(raw, &num); err != nil {
		return 0, err
	}
	text := num.String()
	if strings.ContainsAny(text, ".eE+") {
		return 0, errors.New("not an integer")
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 || value > math.MaxInt {
		return 0, errors.New("out of range")
	}
	return value, nil
}

func CreateQuotaExportJob(c *gin.Context) {
	access := logExportRequestAccess(c)
	filter, start, end, opts, err := parseQuotaExportRequest(c, access.Role)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	setting := operation_setting.GetLogExportSetting()
	if !setting.Enabled {
		common.ApiErrorI18n(c, i18n.MsgLogExportDisabled)
		return
	}
	if !model.LogExportAvailable() {
		common.ApiErrorI18n(c, i18n.MsgLogExportUnavailable)
		return
	}
	if start <= 0 || end <= 0 || end < start || end > 253402300799 {
		if start <= 0 || end <= 0 {
			common.ApiErrorI18n(c, i18n.MsgLogExportRangeRequired)
			return
		}
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeInvalid)
		return
	}
	maxRange := setting.GetAdminMaxRangeSec()
	if end-start > maxRange {
		common.ApiErrorI18n(c, i18n.MsgLogExportRangeTooLong, map[string]any{"Days": maxRange / 86400})
		return
	}
	job := &model.LogExportJob{
		DataSource:   model.LogExportDataQuota,
		JobID:        model.NewLogExportJobID(),
		UserID:       access.UserID,
		Username:     c.GetString("username"),
		CreatorRole:  access.Role,
		QuotaFilters: filter,
		Options:      opts,
		Lang:         i18n.GetLangFromContext(c),
		Format:       model.LogExportFormatCSVGz,
		Mode:         model.LogExportModeDetail,
		Columns:      model.QuotaExportColumns(access.Role),
		Filters:      model.LogExportFilter{StartTimestamp: start, EndTimestamp: end},
	}
	if view, _ := access.Permissions(job); !view {
		respondLogExportManagementError(c, model.ErrEmployeeExportAccess)
		return
	}
	submitLogExportJob(c, job)
}
