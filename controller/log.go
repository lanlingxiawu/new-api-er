package controller

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

// logTypeText 将日志类型数值转换为可读文本，用于导出。
func logTypeText(logType int) string {
	switch logType {
	case model.LogTypeTopup:
		return "充值"
	case model.LogTypeConsume:
		return "消费"
	case model.LogTypeManage:
		return "管理"
	case model.LogTypeSystem:
		return "系统"
	case model.LogTypeError:
		return "错误"
	case model.LogTypeRefund:
		return "退款"
	default:
		return "未知"
	}
}

// retryChainText 从日志 Other 中解析渠道重试链（admin_info.use_channel），
// 对应前端使用日志表格的「重试」列；无数据时返回空串。
func retryChainText(other string) string {
	if other == "" {
		return ""
	}
	m, err := common.StrToMap(other)
	if err != nil || m == nil {
		return ""
	}
	adminInfo, ok := m["admin_info"].(map[string]interface{})
	if !ok {
		return ""
	}
	useChannel, ok := adminInfo["use_channel"].([]interface{})
	if !ok || len(useChannel) == 0 {
		return ""
	}
	parts := make([]string, 0, len(useChannel))
	for _, v := range useChannel {
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.Join(parts, "->")
}

// exportLogsExcel 是 ExportAllLogs / ExportUserLogs 的公共实现，导出为 xlsx。
// userId > 0 时仅导出该用户日志（自助导出）；username 在自助导出时应为空。
func exportLogsExcel(c *gin.Context, userId int) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")

	// 必须选择时间范围，且跨度不能超过 1 个月（参照 GetUserQuotaDates）。
	if startTimestamp == 0 || endTimestamp == 0 {
		common.ApiErrorMsg(c, "请选择导出的时间范围")
		return
	}
	if endTimestamp < startTimestamp {
		common.ApiErrorMsg(c, "结束时间不能早于开始时间")
		return
	}
	if endTimestamp-startTimestamp > 2592000 {
		common.ApiErrorMsg(c, "导出时间跨度不能超过 1 个月")
		return
	}

	// 自助导出忽略 username 过滤，强制锁定当前用户。
	if userId > 0 {
		username = ""
	}
	// 渠道、重试列在前端仅管理员可见，且普通日志接口会剥离 admin_info；
	// 自助导出必须同样隐藏，避免普通用户拿到内部渠道路由信息。
	isAdmin := userId == 0

	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			common.SysError("export logs close file failed: " + err.Error())
		}
	}()

	// 表头顺序与文案对齐前端（classic）使用日志表格。
	header := []interface{}{
		"时间", "渠道", "用户", "令牌", "分组", "类型", "模型",
		"用时/首字", "输入", "输出", "花费", "IP", "重试", "详情",
	}

	// Excel 单表最大 1048576 行（含表头），超出则自动写入新的 Sheet。
	const sheetRowLimit = 1048576

	var (
		sw      *excelize.StreamWriter
		sheetNo int
		rowIdx  int
	)

	// newSheet 切换到新的工作表：先 flush 上一个表，再建表并写入表头。
	newSheet := func() error {
		if sw != nil {
			if err := sw.Flush(); err != nil {
				return err
			}
		}
		sheetNo++
		sheetName := fmt.Sprintf("Sheet%d", sheetNo)
		// 第 1 个表用 NewFile 默认创建的 "Sheet1"，其余需先创建。
		if sheetNo > 1 {
			if _, err := f.NewSheet(sheetName); err != nil {
				return err
			}
		}
		var err error
		// StreamWriter 将行写入临时文件，避免整月数据占用内存。
		if sw, err = f.NewStreamWriter(sheetName); err != nil {
			return err
		}
		if err = sw.SetRow("A1", header); err != nil {
			return err
		}
		rowIdx = 2 // 数据从第 2 行开始
		return nil
	}

	if err := newSheet(); err != nil {
		common.ApiError(c, err)
		return
	}

	exportErr := model.ExportLogs(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group, userId,
		func(batch []*model.Log) error {
			for _, l := range batch {
				if rowIdx > sheetRowLimit {
					if err := newSheet(); err != nil {
						return err
					}
				}
				cell, err := excelize.CoordinatesToCellName(1, rowIdx)
				if err != nil {
					return err
				}
				// 花费换算成美元，四舍五入到 6 位小数，对齐前端 renderQuota(quota, 6)。
				costUSD := math.Round(common.QuotaToUSD(int64(l.Quota))*1e6) / 1e6
				// 渠道、重试仅管理员可见，自助导出置空。
				var channelCell interface{} = ""
				retryCell := ""
				if isAdmin {
					channelCell = l.ChannelId
					retryCell = retryChainText(l.Other)
				}
				row := []interface{}{
					time.Unix(l.CreatedAt, 0).Format("2006-01-02 15:04:05"),
					channelCell,
					l.Username,
					l.TokenName,
					l.Group,
					logTypeText(l.Type),
					l.ModelName,
					l.UseTime,
					l.PromptTokens,
					l.CompletionTokens,
					costUSD,
					l.Ip,
					retryCell,
					l.Content,
				}
				if err := sw.SetRow(cell, row); err != nil {
					return err
				}
				rowIdx++
			}
			return nil
		})
	if exportErr != nil {
		// 响应头尚未发出，可正常返回 JSON 错误。
		common.ApiError(c, exportErr)
		return
	}
	// flush 最后一个工作表。
	if err := sw.Flush(); err != nil {
		common.ApiError(c, err)
		return
	}

	filename := fmt.Sprintf("logs_%d_%d.xlsx", startTimestamp, endTimestamp)
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	if _, err := f.WriteTo(c.Writer); err != nil {
		// 响应已开始写出，无法再返回 JSON 错误，仅记录日志。
		common.SysError("export logs write failed: " + err.Error())
	}
}

// ExportAllLogs 管理员导出全站日志（xlsx）。
func ExportAllLogs(c *gin.Context) {
	exportLogsExcel(c, 0)
}

// ExportUserLogs 普通用户导出自己的日志（xlsx）。
func ExportUserLogs(c *gin.Context) {
	userId := c.GetInt("id")
	if userId <= 0 {
		// 防御性校验：避免 userId 为 0 时退化为导出全站日志。
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "无效的用户",
		})
		return
	}
	exportLogsExcel(c, userId)
}

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	logId, _ := strconv.Atoi(c.Query("log_id"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, total, err := model.GetAllLogs(logType, startTimestamp, endTimestamp, modelName, username, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), channel, logId, group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	logId, _ := strconv.Atoi(c.Query("log_id"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, total, err := model.GetUserLogs(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), logId, group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func getEmployeeCustomerLogFilter(c *gin.Context) model.EmployeeCustomerLogFilter {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	channel, _ := strconv.Atoi(c.Query("channel"))
	logId, _ := strconv.Atoi(c.Query("log_id"))
	return model.EmployeeCustomerLogFilter{
		EmployeeUserId:    c.GetInt("id"),
		CustomerUserId:    customerUserId,
		LogType:           logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         c.Query("model_name"),
		Username:          c.Query("username"),
		TokenName:         c.Query("token_name"),
		Channel:           channel,
		LogId:             logId,
		Group:             c.Query("group"),
		RequestId:         c.Query("request_id"),
		UpstreamRequestId: c.Query("upstream_request_id"),
		StartIdx:          pageInfo.GetStartIdx(),
		PageSize:          pageInfo.GetPageSize(),
	}
}

func GetEmployeeCustomerLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	if !model.IsEmployee(c.GetInt("id")) {
		common.ApiError(c, errNotEmployee)
		return
	}
	filter := getEmployeeCustomerLogFilter(c)
	logs, total, err := model.GetEmployeeCustomerLogs(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		c.JSON(200, gin.H{
			"success": false,
			"message": "无效的令牌",
		})
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

// UpstreamLogQueryCapabilityHeader 标记本实例的令牌日志查询接口原生支持完整筛选，
// 下游 new-api 实例据此判断可执行精确/筛选查询，而非仅拉取近期日志降级过滤。
const UpstreamLogQueryCapabilityHeader = "X-NewAPI-Log-Query"
const UpstreamLogQueryCapabilityValue = "filters-v1"

// GetLogByKeyQuery 是 /api/log/token/query 的处理器：在 TokenAuthReadOnly 之后，
// 强制以当前认证令牌的 token_id 为作用域，叠加与通用日志查询一致的筛选条件返回分页日志。
// 该接口只读、只暴露当前令牌自身的日志，绝不允许扩大到其他令牌。
func GetLogByKeyQuery(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的令牌",
		})
		return
	}
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	logId, _ := strconv.Atoi(c.Query("log_id"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")

	// 声明本实例原生支持完整筛选，供下游实例区分精确查询与近期降级。
	c.Header(UpstreamLogQueryCapabilityHeader, UpstreamLogQueryCapabilityValue)

	logs, total, err := model.GetLogByTokenIdWithFilters(tokenId, logType, startTimestamp, endTimestamp,
		modelName, username, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), channel, logId, group,
		requestId, upstreamRequestId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	stat, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	quotaNum, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum.Quota,
			"rpm":   quotaNum.Rpm,
			"tpm":   quotaNum.Tpm,
			//"token": tokenNum,
		},
	})
	return
}

func GetEmployeeCustomerLogsStat(c *gin.Context) {
	if !model.IsEmployee(c.GetInt("id")) {
		common.ApiError(c, errNotEmployee)
		return
	}
	filter := getEmployeeCustomerLogFilter(c)
	stat, err := model.SumEmployeeCustomerUsedQuota(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
}
