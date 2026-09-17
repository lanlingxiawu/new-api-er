package model

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"not null"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string  `json:"other"`
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:text"`
	//MaxInputTokens     *int    `json:"max_input_tokens" gorm:"default:0"`
	StatusCodeMapping *string `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority          *int64  `json:"priority" gorm:"bigint;default:0"`
	AutoBan           *int    `json:"auto_ban" gorm:"default:1"`
	OtherInfo         string  `json:"other_info"`
	Tag               *string `json:"tag" gorm:"index"`
	Setting           *string `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride     *string `json:"param_override" gorm:"type:text"`
	HeaderOverride    *string `json:"header_override" gorm:"type:text"`
	Remark            *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	// add after v0.8.5
	ChannelInfo ChannelInfo `json:"channel_info" gorm:"type:json"`

	OtherSettings string `json:"settings" gorm:"column:settings"` // 其他设置，存储azure版本等不需要检索的信息，详见dto.ChannelOtherSettings

	// cache info
	Keys []string `json:"-" gorm:"-"`

	// AccountBalance 渠道账号余额，从 Redis 按需填充，不持久化到 channels 表
	AccountBalance *ChannelAccountBalance `json:"account_balance,omitempty" gorm:"-"`
	// AccountBalanceConfigured 是否已配置账号余额查询（Token+UserID 均非空），控制器填充，不持久化
	AccountBalanceConfigured bool `json:"account_balance_configured" gorm:"-"`

	// CostRatio 渠道成本系数，不持久化到 channels 表（实际存于 ChannelCostConfig）。
	// 仅用于在渠道增改接口中透传该值，由控制器同步到 ChannelCostConfig；
	// GetChannel 读取时回填，供前端编辑表单预填。指针区分「未提供(nil)」与「显式设置」。
	CostRatio *float64 `json:"cost_ratio,omitempty" gorm:"-"`

	// —— 渠道每日金额上限（docs/design/channel-daily-quota-limit.md）——
	// 配置列用真实列而非 setting JSON：筛选/排序需要进 SQL，批量设置需要原子 UPDATE。
	// DailyQuotaLimit 每日上限（quota 单位，与 UsedQuota 同单位）。<=0 表示无上限。
	DailyQuotaLimit int64 `json:"daily_quota_limit" gorm:"bigint;default:0;index"`
	// DailyLimitAutoRecover 是否自动恢复启用。沿用本表 AutoBan 的 *int 跨库布尔写法。
	DailyLimitAutoRecover *int `json:"daily_limit_auto_recover" gorm:"default:1"`
	// DailyLimitRecoverMinutes 限时恢复间隔（分钟）。0 = 按自然日：次日零点恢复或不恢复，由
	// DailyLimitAutoRecover 决定；>0 = 限时模式：达到上限后 N 分钟自动恢复并开启新一轮计数。
	DailyLimitRecoverMinutes int `json:"daily_limit_recover_minutes" gorm:"default:0"`
	// DailyLimitPeriodStart 限时模式当前一轮的开始时刻（unix 秒），服务端维护、只读。
	// 由限时恢复、手动启用、修改恢复间隔时写入；按日模式下不使用。
	DailyLimitPeriodStart int64 `json:"daily_limit_period_start" gorm:"bigint;default:0"`
	// DailyLimitDisabledAt 本功能自动禁用的时刻；0 表示当前不是被本功能禁用。
	// 不变式：DailyLimitDisabledAt > 0 <=> 最近一次禁用来自每日上限。
	DailyLimitDisabledAt int64 `json:"daily_limit_disabled_at" gorm:"bigint;default:0"`
	// DailyLimitDisabledDate 触发禁用时对应的 StatDate（配置时区当日 00:00），恢复判定用。
	DailyLimitDisabledDate int64 `json:"daily_limit_disabled_date" gorm:"bigint;default:0"`

	// DailyUsage 渠道今日用量，列表接口按需批量填充，不持久化。
	DailyUsage *ChannelDailyUsageView `json:"daily_usage,omitempty" gorm:"-"`
}

// MaxDailyLimitRecoverMinutes 限时恢复间隔上限：7 天。
const MaxDailyLimitRecoverMinutes = 10080

// MinDailyQuotaLimit 返回允许配置的最小上限，避免误填极小值导致渠道第一个请求就被禁用。
//
// 取「一分钱」而不是一个写死的常量：quota 与金额的换算由 common.QuotaPerUnit 决定，
// 而它是可配置的（Tokens 模式 / 自定义货币会改它）。写死 1 quota 等于没有下限——
// 默认换算下 1 quota ≈ $0.000002。
//
// 只在保存时校验，不影响已有配置：LoadDailyLimitConfigs 不做校验，改小 QuotaPerUnit
// 不会让历史上限突然失效。
func MinDailyQuotaLimit() int64 {
	minimum := int64(common.QuotaPerUnit / 100)
	if minimum < 1 {
		return 1
	}
	return minimum
}

// GetDailyLimitAutoRecover 返回是否自动恢复；未配置（nil）视为 true。
func (channel *Channel) GetDailyLimitAutoRecover() bool {
	if channel.DailyLimitAutoRecover == nil {
		return true
	}
	return *channel.DailyLimitAutoRecover == 1
}

// IsDailyLimitTimed 报告渠道是否处于限时恢复模式（达到上限后 N 分钟恢复）。
func (channel *Channel) IsDailyLimitTimed() bool {
	return channel.DailyLimitRecoverMinutes > 0
}

// NormalizeDailyLimitRecovery 规整新建渠道的恢复配置：限时模式隐含自动恢复，并从当前时刻开启第一轮。
// 按日模式不使用轮次，period_start 归零。客户端传入的 period_start 一律不采信。
func (channel *Channel) NormalizeDailyLimitRecovery(now int64) {
	if channel.DailyLimitRecoverMinutes > 0 {
		one := 1
		channel.DailyLimitAutoRecover = &one
		channel.DailyLimitPeriodStart = now
		return
	}
	channel.DailyLimitPeriodStart = 0
}

const ChannelStatusReasonAllKeysDisabled = "All keys are disabled"

type ChannelInfo struct {
	IsMultiKey             bool                  `json:"is_multi_key"`                        // 是否多Key模式
	MultiKeySize           int                   `json:"multi_key_size"`                      // 多Key模式下的Key数量
	MultiKeyStatusList     map[int]int           `json:"multi_key_status_list"`               // key状态列表，key index -> status
	MultiKeyDisabledReason map[int]string        `json:"multi_key_disabled_reason,omitempty"` // key禁用原因列表，key index -> reason
	MultiKeyDisabledTime   map[int]int64         `json:"multi_key_disabled_time,omitempty"`   // key禁用时间列表，key index -> time
	MultiKeyPollingIndex   int                   `json:"multi_key_polling_index"`             // 多Key模式下轮询的key索引
	MultiKeyMode           constant.MultiKeyMode `json:"multi_key_mode"`
}

type ChannelSortOptions struct {
	SortBy    string
	SortOrder string
	IDSort    bool
	// StatDate 是今日用量 JOIN 的日期条件；仅在按每日用量排序时需要，为 0 表示不启用
	// 表达式排序（Tag 模式与不支持的排序键都是 0）。
	StatDate int64
}

var channelSortColumns = map[string]string{
	"id":            "id",
	"name":          "name",
	"priority":      "priority",
	"balance":       "balance",
	"response_time": "response_time",
	"test_time":     "test_time",
}

func NewChannelSortOptions(sortBy string, sortOrder string, idSort bool) ChannelSortOptions {
	normalizedSortBy := strings.ToLower(strings.TrimSpace(sortBy))
	normalizedSortOrder := strings.ToLower(strings.TrimSpace(sortOrder))
	_, isColumnSort := channelSortColumns[normalizedSortBy]
	isUsageSort := channelSortNeedsUsage(normalizedSortBy)
	if !isColumnSort && !isUsageSort {
		normalizedSortBy = ""
		normalizedSortOrder = ""
	} else if normalizedSortOrder != "asc" {
		normalizedSortOrder = "desc"
	}

	return ChannelSortOptions{
		SortBy:    normalizedSortBy,
		SortOrder: normalizedSortOrder,
		IDSort:    idSort,
	}
}

func (options ChannelSortOptions) Apply(query *gorm.DB) *gorm.DB {
	// 每日用量 / 使用率是表达式排序，clause.OrderByColumn 表达不了，单独处理。
	// TagMode 下不支持（见 ChannelListQueryOptions.TagMode 的说明），此时 StatDate 为 0。
	if options.StatDate > 0 {
		if next, ok := applyDailyUsageOrder(query, options.SortBy, options.StatDate, options.SortOrder != "asc"); ok {
			return next
		}
	}
	if columnName, ok := channelSortColumns[options.SortBy]; ok {
		return query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: columnName},
			Desc:   options.SortOrder != "asc",
		})
	}
	if options.IDSort {
		return query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: "id"},
			Desc:   true,
		})
	}
	return query.Order(clause.OrderByColumn{
		Column: clause.Column{Name: "priority"},
		Desc:   true,
	})
}

func resolveChannelSortOptions(idSort bool, sortOptions []ChannelSortOptions) ChannelSortOptions {
	if len(sortOptions) == 0 {
		return NewChannelSortOptions("", "", idSort)
	}
	options := sortOptions[0]
	options.IDSort = options.IDSort || idSort
	return options
}

func NormalizeChannelGroupFilter(group string) string {
	group = strings.TrimSpace(group)
	if group == "" || strings.EqualFold(group, "all") || strings.EqualFold(group, "null") {
		return ""
	}
	return group
}

func channelGroupFilterCondition() string {
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		return `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ? ESCAPE '!'`
	}
	return `(',' || ` + commonGroupCol + ` || ',') LIKE ? ESCAPE '!'`
}

func channelGroupFilterPattern(group string) string {
	group = strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	).Replace(group)
	return "%," + group + ",%"
}

func ApplyChannelGroupFilter(query *gorm.DB, group string) *gorm.DB {
	group = NormalizeChannelGroupFilter(group)
	if group == "" {
		return query
	}
	return query.Where(channelGroupFilterCondition(), channelGroupFilterPattern(group))
}

// Value implements driver.Valuer interface
// 必须返回 string 而非 []byte:PG simple protocol 下 []byte 参数按 bytea
// 编码,写 json 列会触发 SQLSTATE 22P02。
func (c ChannelInfo) Value() (driver.Value, error) {
	b, err := common.Marshal(&c)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner interface
func (c *ChannelInfo) Scan(value any) error {
	return common.Unmarshal(jsonScanBytes(value), c)
}

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	trimmed := strings.TrimSpace(channel.Key)
	// If the key starts with '[', try to parse it as a JSON array (e.g., for Vertex AI scenarios)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			res := make([]string, len(arr))
			for i, v := range arr {
				res[i] = string(v)
			}
			return res
		}
	}
	// Otherwise, fall back to splitting by newline
	keys := strings.Split(strings.Trim(channel.Key, "\n"), "\n")
	return keys
}

func (channel *Channel) GetNextEnabledKey() (string, int, *types.NewAPIError) {
	// If not in multi-key mode, return the original key string directly.
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	// Obtain all keys (split by \n)
	keys := channel.GetKeys()
	if len(keys) == 0 {
		// No keys available, return error, should disable the channel
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	statusList := channel.ChannelInfo.MultiKeyStatusList
	// helper to get key status, default to enabled when missing
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	// Collect indexes of enabled keys
	enabledIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) == common.ChannelStatusEnabled {
			enabledIdx = append(enabledIdx, i)
		}
	}
	// If no specific status list or none enabled, return an explicit error so caller can
	// properly handle a channel with no available keys (e.g. mark channel disabled).
	// Returning the first key here caused requests to keep using an already-disabled key.
	if len(enabledIdx) == 0 {
		return "", 0, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		// Randomly pick one enabled key
		selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModePolling:
		// Use channel-specific lock to ensure thread-safe polling

		channelInfo, err := CacheGetChannelInfo(channel.Id)
		if err != nil {
			return "", 0, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		defer func() {
			if common.DebugEnabled {
				logger.LogDebug(nil, "channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex)
			}
			if !common.MemoryCacheEnabled {
				_ = channel.SaveChannelInfo()
			} else {
				// CacheUpdateChannel(channel)
			}
		}()
		// Start from the saved polling index and look for the next enabled key
		start := channelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := range keys {
			idx := (start + i) % len(keys)
			if getStatus(idx) == common.ChannelStatusEnabled {
				// update polling index for next call (point to the next position)
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		// Fallback – should not happen, but return first enabled key
		return keys[enabledIdx[0]], enabledIdx[0], nil
	default:
		// Unknown mode, default to first enabled key (or original key string)
		return keys[enabledIdx[0]], enabledIdx[0], nil
	}
}

func (channel *Channel) SaveChannelInfo() error {
	return DB.Model(channel).Update("channel_info", channel.ChannelInfo).Error
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{}
	}
	groups := strings.Split(strings.Trim(channel.Group, ","), ",")
	for i, group := range groups {
		groups[i] = strings.TrimSpace(group)
	}
	return groups
}

func (channel *Channel) GetOtherInfo() map[string]any {
	otherInfo := make(map[string]any)
	if channel.OtherInfo != "" {
		err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		}
	}
	return otherInfo
}

func (channel *Channel) SetOtherInfo(otherInfo map[string]any) {
	otherInfoBytes, err := json.Marshal(otherInfo)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		return
	}
	channel.OtherInfo = string(otherInfoBytes)
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

func (channel *Channel) Save() error {
	return DB.Save(channel).Error
}

// saveStatusState persists only the fields owned by the channel status flow.
// Keeping this allowlist here prevents a stale channel snapshot from
// overwriting credentials, accounting counters, or channel configuration.
func (channel *Channel) saveStatusState() error {
	return channel.saveStatusStateWith(nil)
}

// saveStatusStateWith 在状态字段之外，把本次状态流转显式改动的每日上限标记（dailyLimitUpdates）
// 写进同一条 UPDATE，避免「状态变了但标记还在」的中间态落库；未改动的标记不写，防止旧快照覆盖。
func (channel *Channel) saveStatusStateWith(dailyLimitUpdates map[string]any) error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	updates := map[string]any{
		"status":     channel.Status,
		"other_info": channel.OtherInfo,
	}
	if channel.ChannelInfo.IsMultiKey {
		updates["channel_info"] = channel.ChannelInfo
	}
	for column, value := range dailyLimitUpdates {
		updates[column] = value
	}
	return DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(updates).Error
}

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := resolveChannelSortOptions(idSort, sortOptions)
	if selectAll {
		err = order.Apply(DB).Find(&channels).Error
	} else {
		err = order.Apply(DB).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

// GetEnabledChannelsForBalanceRefresh returns enabled channels with the upstream
// `key` column omitted. The scheduled account-balance refresh authenticates with the
// per-channel AccountBalanceToken stored in settings, never the upstream key, so the
// (encrypted) key material is intentionally not loaded into memory. The enabled
// filter is pushed into SQL to avoid scanning disabled channels.
func GetEnabledChannelsForBalanceRefresh() ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("status = ?", common.ChannelStatusEnabled).Omit("key").Find(&channels).Error
	return channels, err
}

func GetChannelsByTag(tag string, idSort bool, selectAll bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	order := resolveChannelSortOptions(idSort, sortOptions)
	query := order.Apply(DB.Where("tag = ?", tag))
	if !selectAll {
		query = query.Omit("key")
	}
	err := query.Find(&channels).Error
	return channels, err
}

// SearchChannels 关键字搜索。limitFilter / statDate 与列表接口共用同一套条件构造
// （ApplyChannelLimitFilter），保证两条路径在每日上限筛选上的行为完全一致。
//
// 注意：本函数的 status / type 过滤仍由 controller 在 Go 侧完成（既有实现），这里不动，
// 那属于独立的搜索路径重构，不在每日上限功能范围内。
func SearchChannels(keyword string, group string, model string, idSort bool, limitFilter string, statDate int64, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := resolveChannelSortOptions(idSort, sortOptions)

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(channels.id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)
	baseQuery = ApplyChannelLimitFilter(baseQuery, limitFilter, statDate)

	// 执行查询
	err := order.Apply(baseQuery).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

// GetChannelById loads a channel directly from the database, bypassing the
// in-memory channel cache.
//
// WARNING: do NOT call this on request hot paths (middleware, distribution,
// relay submit/retry, polling). Every call is a synchronous DB query and will
// not see cache-only state. Use CacheGetChannel instead: it serves from the
// in-memory cache and falls back to this function automatically when
// MemoryCacheEnabled is false. Direct use is appropriate only where fresh DB
// state is required, e.g. admin CRUD, channel testing, or cache (re)building.
func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(channel, "id = ?", id).Error
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func BatchInsertChannels(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 使用原生切片分片（共享底层数组），确保 tx.Create 生成的自增 ID
	// 回写到调用方传入的 channels，便于创建后按渠道 ID 同步成本配置等。
	const chunkSize = 50
	for start := 0; start < len(channels); start += chunkSize {
		end := start + chunkSize
		if end > len(channels) {
			end = len(channels)
		}
		chunk := channels[start:end]
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for i := range chunk {
			if err := chunk[i].AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit().Error
}

func BatchDeleteChannels(ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// 使用事务 分批删除channel表和abilities表
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	allNames := make(map[int]string, len(ids))
	var deletedCount int64
	for _, chunk := range lo.Chunk(ids, 200) {
		// 删除前捕获该批渠道名，名称快照移到删除后异步处理。
		for id, name := range captureChannelNamesForSnapshot(tx, chunk) {
			allNames[id] = name
		}
		result := tx.Where("id in (?)", chunk).Delete(&Channel{})
		if result.Error != nil {
			tx.Rollback()
			return 0, result.Error
		}
		deletedCount += result.RowsAffected
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	finalizeChannelDeletionAsync(allNames, ids)
	return deletedCount, nil
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return int(*channel.Weight)
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	url := *channel.BaseURL
	if url == "" {
		url = constant.GetChannelBaseURL(channel.Type)
	}
	return url
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func (channel *Channel) Insert() error {
	var err error
	err = DB.Create(channel).Error
	if err != nil {
		return err
	}
	err = channel.AddAbilities(nil)
	return err
}

func (channel *Channel) Update() error {
	// If this is a multi-key channel, recalculate MultiKeySize based on the current key list to avoid inconsistency after editing keys
	if channel.ChannelInfo.IsMultiKey {
		var keyStr string
		if channel.Key != "" {
			keyStr = channel.Key
		} else {
			// If key is not provided, read the existing key from the database
			if existing, err := GetChannelById(channel.Id, true); err == nil {
				keyStr = existing.Key
			}
		}
		// Parse the key list (supports newline separation or JSON array)
		keys := []string{}
		if keyStr != "" {
			trimmed := strings.TrimSpace(keyStr)
			if strings.HasPrefix(trimmed, "[") {
				var arr []json.RawMessage
				if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
					keys = make([]string, len(arr))
					for i, v := range arr {
						keys[i] = string(v)
					}
				}
			}
			if len(keys) == 0 { // fallback to newline split
				keys = strings.Split(strings.Trim(keyStr, "\n"), "\n")
			}
		}
		channel.ChannelInfo.MultiKeySize = len(keys)
		// Clean up status data that exceeds the new key count to prevent index out of range
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			for idx := range channel.ChannelInfo.MultiKeyStatusList {
				if idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyStatusList, idx)
				}
			}
		}
	}
	var err error
	// 限时恢复的两列只经 DailyLimitEdit 写入：恢复间隔变化时要在同一条 UPDATE 里据旧值
	// 判断是否开启新一轮，这里若先把新间隔写进去，那次判断就读不到旧值了；period_start 则是
	// 服务端维护的只读列。
	err = DB.Model(channel).Omit("daily_limit_recover_minutes", "daily_limit_period_start").Updates(channel).Error
	if err != nil {
		return err
	}
	DB.Model(channel).First(channel, "id = ?", channel.Id)
	err = channel.UpdateAbilities(nil)
	return err
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: common.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update balance: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) Delete() error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	// 删除前捕获渠道名（删后 channels 表已无），名称快照移到删除后异步处理。
	names := captureChannelNamesForSnapshot(tx, []int{channel.Id})
	if err := tx.Delete(channel).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	// 名称快照 + 余额缓存清理移出删除流程，删除成功后异步 best-effort 处理。
	finalizeChannelDeletionAsync(names, []int{channel.Id})
	return nil
}

// captureChannelNamesForSnapshot 在删除前读取 id->name（已 Trim、丢弃空名/0 id）。
// 必须在渠道行被删除之前调用：渠道是硬删除，删后 channels 表已无此名。
// 仅只读 SELECT（主键 IN 查询，不加行锁），结果交给删除完成后的异步 finalize 使用。
func captureChannelNamesForSnapshot(q *gorm.DB, ids []int) map[int]string {
	result := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return result
	}
	var rows []struct {
		Id   int
		Name string
	}
	if err := q.Model(&Channel{}).Select("id, name").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		common.SysError(fmt.Sprintf("captureChannelNamesForSnapshot: %v", err))
		return result
	}
	for _, row := range rows {
		if name := strings.TrimSpace(row.Name); row.Id != 0 && name != "" {
			result[row.Id] = name
		}
	}
	return result
}

// finalizeChannelDeletion 渠道删除成功后的非关键收尾，已移出删除主流程/事务：
//  1. 把删除前捕获的渠道名快照回写日聚合小表 platform_channel_daily_stats，
//     保住已删渠道在报表/筛选下拉里的名字（在世期间计费刷盘通常已写入，此处是兜底）；
//  2. 清理 Redis 中该渠道的上游账户余额缓存。
//
// 全程 best-effort：单项失败只记日志，不影响（也无法回滚）已提交的删除。
// 之所以移出删除事务：platform_channel_daily_stats 是 relay 计费刷盘写的共享表，
// 在删除事务内对它做 UPDATE 会与刷盘抢锁；改为删除后异步处理，删除请求不再为此阻塞。
func finalizeChannelDeletion(names map[int]string, ids []int) {
	for id, name := range names {
		if err := DB.Model(&PlatformChannelDailyStat{}).
			Where("channel_id = ? AND (channel_name = '' OR channel_name IS NULL)", id).
			Update("channel_name", name).Error; err != nil {
			common.SysError(fmt.Sprintf("finalizeChannelDeletion: snapshot channel_id=%d failed: %v", id, err))
		}
	}
	for _, id := range ids {
		if err := DeleteChannelAccountBalance(id); err != nil {
			common.SysError(fmt.Sprintf("finalizeChannelDeletion: delete account balance cache channel_id=%d failed: %v", id, err))
		}
	}
	// 每日上限的用量行。累计器里残留的增量可能再写回一行孤儿记录，由周期性孤儿清理兜底。
	if deleted, err := DeleteChannelLimitPeriodUsageByChannelIds(ids); err != nil {
		common.SysError(fmt.Sprintf("finalizeChannelDeletion: delete limit period usage rows failed: %v", err))
	} else if deleted > 0 {
		common.SysLog(fmt.Sprintf("finalizeChannelDeletion: removed %d channel limit period usage row(s)", deleted))
	}
	if deleted, err := DeleteChannelDailyUsageByChannelIds(ids); err != nil {
		common.SysError(fmt.Sprintf("finalizeChannelDeletion: delete daily usage rows failed: %v", err))
	} else if deleted > 0 {
		common.SysLog(fmt.Sprintf("finalizeChannelDeletion: removed %d channel daily usage row(s)", deleted))
	}
}

// finalizeChannelDeletionAsync 是 finalizeChannelDeletion 的 fire-and-forget 包装，带 panic 兜底。
func finalizeChannelDeletionAsync(names map[int]string, ids []int) {
	if len(names) == 0 && len(ids) == 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.SysError(fmt.Sprintf("finalizeChannelDeletionAsync panic: %v", r))
			}
		}()
		finalizeChannelDeletion(names, ids)
	}()
}

var channelStatusLock sync.Mutex

// channelPollingLocks stores locks for each channel.id to ensure thread-safe polling
var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	// Create new lock for this channel
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist
// This is optional and can be called periodically to prevent memory leaks
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	DB.Model(&Channel{}).Pluck("id", &activeChannelIds)

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value any) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string) {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		channel.Status = status
	} else {
		keyIndex := -1
		for i, key := range keys {
			if key == usingKey {
				keyIndex = i
				break
			}
		}
		if keyIndex < 0 {
			if usingKey != "" {
				common.SysLog(fmt.Sprintf("failed to update multi-key status: channel_id=%d, using key not found", channel.Id))
				return
			}
			channel.Status = status
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			return
		}
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if status == common.ChannelStatusEnabled {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
		} else {
			channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
			if channel.ChannelInfo.MultiKeyDisabledReason == nil {
				channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime == nil {
				channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
			channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
		}
		if !hasEnabledMultiKey(keys, channel.ChannelInfo.MultiKeyStatusList) {
			channel.Status = common.ChannelStatusAutoDisabled
			info := channel.GetOtherInfo()
			info["status_reason"] = ChannelStatusReasonAllKeysDisabled
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
		} else if status == common.ChannelStatusEnabled {
			channel.Status = common.ChannelStatusEnabled
		}
	}
}

func hasEnabledMultiKey(keys []string, statusList map[int]int) bool {
	for i := range keys {
		if statusList == nil {
			return true
		}
		status, ok := statusList[i]
		if !ok || status == common.ChannelStatusEnabled {
			return true
		}
	}
	return false
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) bool {
	// 任何「非每日上限」的状态变更都先清除限额禁用标记，维持不变式
	// daily_limit_disabled_at > 0 <=> 最近一次禁用来自每日上限。必须放在「目标状态与当前相同」
	// 的提前返回之前：限额禁用后在途请求报错再写一次 status=3 时，标记不清就会把一个真坏的
	// 渠道自动恢复。
	clearDailyLimitMarksIfPresent(channelId)
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}

	// ChannelInfo stores both multi-key status and the polling cursor. Hold the
	// same per-channel lock from the first read through persistence so neither
	// writer can save a stale JSON snapshot over the other.
	pollingLock := GetChannelPollingLock(channelId)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	if common.MemoryCacheEnabled {
		channelCache, _ := CacheGetChannel(channelId)
		if channelCache == nil {
			return false
		}
		if channelCache.ChannelInfo.IsMultiKey {
			beforeStatus := channelCache.Status
			// 如果是多Key模式，更新缓存中的状态
			handlerMultiKeyUpdate(channelCache, usingKey, status, reason)
			if beforeStatus != channelCache.Status {
				CacheUpdateChannelStatus(channelId, channelCache.Status)
			}
			//CacheUpdateChannel(channelCache)
			//return true
		} else {
			// 如果缓存渠道存在，且状态已是目标状态，直接返回
			if channelCache.Status == status {
				return false
			}
			CacheUpdateChannelStatus(channelId, status)
		}
	}

	shouldUpdateAbilities := false
	defer func() {
		if shouldUpdateAbilities {
			err := UpdateAbilityStatus(channelId, status == common.ChannelStatusEnabled)
			if err != nil {
				common.SysLog(fmt.Sprintf("failed to update ability status: channel_id=%d, error=%v", channelId, err))
			}
		}
	}()
	channel, err := GetChannelById(channelId, true)
	if err != nil {
		return false
	} else {
		// A manual channel operation must replace the exhaustion reason even
		// when the status value is already manually disabled.
		overridesKeyExhaustion := channel.ChannelInfo.IsMultiKey && usingKey == "" &&
			status == common.ChannelStatusManuallyDisabled && reason != ChannelStatusReasonAllKeysDisabled &&
			channel.GetOtherInfo()["status_reason"] == ChannelStatusReasonAllKeysDisabled
		if channel.Status == status && !overridesKeyExhaustion {
			return false
		}

		dailyLimitUpdates := map[string]any{}
		if channel.ChannelInfo.IsMultiKey {
			beforeStatus := channel.Status
			handlerMultiKeyUpdate(channel, usingKey, status, reason)
			if beforeStatus != channel.Status {
				shouldUpdateAbilities = true
			}
		} else {
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			channel.Status = status
			// 与状态写入同一次保存，保证「状态变了但标记还在」这种中间态不会落库。
			channel.DailyLimitDisabledAt = 0
			channel.DailyLimitDisabledDate = 0
			dailyLimitUpdates["daily_limit_disabled_at"] = 0
			dailyLimitUpdates["daily_limit_disabled_date"] = 0
			shouldUpdateAbilities = true
		}
		// 限时恢复模式下，任何「变为启用」（管理员手动启用、按 key 恢复等）都开启新一轮：
		// 否则本轮用量仍压在上限之上，下一笔请求就会再次触发禁用。
		if shouldUpdateAbilities && channel.Status == common.ChannelStatusEnabled && channel.IsDailyLimitTimed() {
			channel.DailyLimitPeriodStart = common.GetTimestamp()
			dailyLimitUpdates["daily_limit_period_start"] = channel.DailyLimitPeriodStart
		}
		err = channel.saveStatusStateWith(dailyLimitUpdates)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channel.Id, status, err))
			return false
		}
	}
	return true
}

func EnableChannelByTag(tag string) error {
	// 同一条 UPDATE 里顺带清零限额禁用标记，见 UpdateChannelStatus 中的同名说明。
	// 只给「原本未启用」的限时渠道开启新一轮，已在运行的渠道保持当前一轮——否则按 Tag 点一次
	// 启用，就会让本轮快跑满的渠道凭空多出一整轮额度。CASE 必须读到旧状态：GORM 按列名排序
	// 生成 SET，daily_limit_period_start 排在 status 之前（MySQL 按书写顺序求值）。
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Updates(map[string]any{
		"status":                    common.ChannelStatusEnabled,
		"daily_limit_disabled_at":   0,
		"daily_limit_disabled_date": 0,
		"daily_limit_period_start": gorm.Expr("CASE WHEN daily_limit_recover_minutes > 0 AND status <> ? THEN ? ELSE daily_limit_period_start END",
			common.ChannelStatusEnabled, common.GetTimestamp()),
	}).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, true)
	return err
}

func DisableChannelByTag(tag string) error {
	// Explicit tag-level disable also cancels automatic restoration for
	// channels that were already disabled because all keys were unavailable.
	var channels []Channel
	if err := DB.Where("tag = ?", tag).Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		if channel.ChannelInfo.IsMultiKey && channel.GetOtherInfo()["status_reason"] == ChannelStatusReasonAllKeysDisabled {
			if !UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual tag operation") {
				return fmt.Errorf("failed to disable channel #%d by tag", channel.Id)
			}
		}
	}
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Updates(map[string]any{
		"status":                    common.ChannelStatusManuallyDisabled,
		"daily_limit_disabled_at":   0,
		"daily_limit_disabled_date": 0,
	}).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, false)
	return err
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	updateData := Channel{}
	shouldReCreateAbilities := false
	updatedTag := tag
	// 如果 newTag 不为空且不等于 tag，则更新 tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
		updatedTag = *newTag
	}
	if modelMapping != nil {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldReCreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}

	err := DB.Model(&Channel{}).Where("tag = ?", tag).Updates(updateData).Error
	if err != nil {
		return err
	}
	if shouldReCreateAbilities {
		channels, err := GetChannelsByTag(updatedTag, false, false)
		if err == nil {
			for _, channel := range channels {
				err = channel.UpdateAbilities(nil)
				if err != nil {
					common.SysLog(fmt.Sprintf("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
	} else {
		err := UpdateAbilityByTag(tag, newTag, priority, weight)
		if err != nil {
			return err
		}
	}
	return nil
}

func UpdateChannelUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel used quota: channel_id=%d, delta_quota=%d, error=%v", id, quota, err))
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var ids []int
	if err := tx.Model(&Channel{}).Where("status = ?", status).Pluck("id", &ids).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	// 删除前捕获渠道名，名称快照移到删除后异步处理。
	names := captureChannelNamesForSnapshot(tx, ids)
	result := tx.Where("status = ?", status).Delete(&Channel{})
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	finalizeChannelDeletionAsync(names, ids)
	return result.RowsAffected, nil
}

func DeleteDisabledChannel() (int64, error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var ids []int
	if err := tx.Model(&Channel{}).
		Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).
		Pluck("id", &ids).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	// 删除前捕获渠道名，名称快照移到删除后异步处理。
	names := captureChannelNamesForSnapshot(tx, ids)
	result := tx.Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Delete(&Channel{})
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	finalizeChannelDeletionAsync(names, ids)
	return result.RowsAffected, nil
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	return GetPaginatedChannelTags(DB.Model(&Channel{}), offset, limit)
}

func GetPaginatedChannelTags(query *gorm.DB, offset int, limit int) ([]*string, error) {
	var tags []*string
	err := query.
		Select("DISTINCT tag").
		Where("tag is not null AND tag != ''").
		Order(clause.OrderByColumn{Column: clause.Column{Name: "tag"}}).
		Offset(offset).
		Limit(limit).
		Find(&tags).Error
	return tags, err
}

func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)

	subQuery := baseQuery.
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := DB.Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (channel *Channel) ValidateSettings() error {
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), channelParams)
		if err != nil {
			return err
		}
	}
	if _, err := common.ParseProxyURLStrict(channelParams.Proxy); err != nil {
		return fmt.Errorf("invalid channel proxy: %w", err)
	}
	if err := channelParams.ValidateHTTPTransport(); err != nil {
		return err
	}
	channelOtherSettings := &dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, channelOtherSettings)
		if err != nil {
			return err
		}
	}
	if err := channelOtherSettings.ValidateToolLossPolicy(); err != nil {
		return err
	}
	if preset := common.GetAdvancedCustomPreset(channel.Type); preset != nil {
		channelOtherSettings.AdvancedCustom = preset
	}
	if constant.IsAdvancedCustomChannel(channel.Type) {
		if channelOtherSettings.AdvancedCustom == nil {
			return fmt.Errorf("advanced_custom is required")
		}
	}
	if channelOtherSettings.AdvancedCustom != nil {
		if err := channelOtherSettings.AdvancedCustom.Validate(); err != nil {
			return err
		}
	}
	if constant.IsAdvancedCustomChannel(channel.Type) && channelOtherSettings.UpstreamModelUpdateCheckEnabled {
		if _, ok := channelOtherSettings.AdvancedCustom.ModelListRoute(); !ok {
			return fmt.Errorf("advanced custom channels require a %s route when upstream model update checks are enabled", dto.AdvancedCustomModelListPath)
		}
	}
	if err := ValidateDailyLimitConfig(channel.DailyQuotaLimit, channel.DailyLimitRecoverMinutes); err != nil {
		return err
	}
	return nil
}

// ValidateDailyLimitConfig 校验每日上限配置。单渠道保存、批量设置、按 Tag 设置共用此函数，
// 保证三条写入路径的校验完全一致。返回的错误串对应 i18n key，由控制器翻译。
func ValidateDailyLimitConfig(limit int64, recoverMinutes int) error {
	if limit < 0 {
		return errors.New(i18n.MsgChannelDailyLimitInvalidAmount)
	}
	if limit > 0 && limit < MinDailyQuotaLimit() {
		return errors.New(i18n.MsgChannelDailyLimitInvalidAmount)
	}
	if recoverMinutes < 0 || recoverMinutes > MaxDailyLimitRecoverMinutes {
		return errors.New(i18n.MsgChannelDailyLimitInvalidRecoverMinutes)
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.Setting = nil // 清空设置以避免后续错误
			_ = channel.Save()    // 保存修改
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.OtherSettings = "{}" // 清空设置以避免后续错误
			_ = channel.Save()           // 保存修改
		}
	}
	if preset := common.GetAdvancedCustomPreset(channel.Type); preset != nil {
		setting.AdvancedCustom = preset
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

func (channel *Channel) GetParamOverride() map[string]any {
	paramOverride := make(map[string]any)
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]any {
	headerOverride := make(map[string]any)
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("id in (?)", ids).Find(&channels).Error
	return channels, err
}

func GetChannelNamesByIds(ids []int) (map[int]string, error) {
	return GetChannelNamesByIdsWithContext(context.Background(), ids)
}

func GetChannelNamesByIdsWithContext(ctx context.Context, ids []int) (map[int]string, error) {
	result := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var rows []struct {
		Id   int
		Name string
	}
	// Unscoped: 已删除渠道的历史消费数据仍需显示渠道名
	err := DB.WithContext(safeDBContext(ctx)).Unscoped().Model(&Channel{}).Select("id, name").Where("id IN ?", ids).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.Id] = row.Name
	}
	return result, nil
}

// ResolveChannelDisplayNamesWithoutLogs 批量解析渠道显示名称，仅经两层：
// 进程内缓存（MemoryCacheEnabled 时）或 channels 表（含已软删，Unscoped）→ platform_channel_daily_stats 名称快照。
// 不回退到 logs 表扫描（logs 的 LIKE '%channel_name%' 是全表过滤，代价高）。
// 解析不到的 id 不会出现在返回 map 中，调用方应保持原值不变。
func ResolveChannelDisplayNamesWithoutLogs(ids []int) map[int]string {
	uniq := make([]int, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	result := make(map[int]string, len(uniq))
	missing := make([]int, 0, len(uniq))
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		for _, id := range uniq {
			if ch, ok := channelsIDM[id]; ok && ch.Name != "" {
				result[id] = ch.Name
			} else {
				missing = append(missing, id)
			}
		}
		channelSyncLock.RUnlock()
	} else {
		r, err := GetChannelNamesByIds(uniq)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to resolve channel names from channels: %v", err))
		} else {
			result = r
		}
		for _, id := range uniq {
			if result[id] == "" {
				missing = append(missing, id)
			}
		}
	}
	if len(missing) == 0 {
		return result
	}
	var rows []struct {
		ChannelId   int
		ChannelName string
	}
	if err := DB.Model(&PlatformChannelDailyStat{}).
		Select("channel_id, MAX(channel_name) as channel_name").
		Where("channel_id IN ? AND channel_name <> ''", missing).
		Group("channel_id").
		Scan(&rows).Error; err != nil {
		common.SysLog(fmt.Sprintf("failed to resolve channel names from daily stats: %v", err))
	} else {
		for _, row := range rows {
			if row.ChannelName != "" {
				result[row.ChannelId] = row.ChannelName
			}
		}
	}
	return result
}

// ResolveChannelDisplayNames 批量解析渠道显示名称（兼容已删除渠道），仅用于按页展示等小批量场景。
// 解析链：channels 表 → platform_channel_daily_stats 名称快照 → logs 快照。
// 渠道删除后 finalizeChannelDeletion（异步）会把名称快照回写日聚合表（在世期间刷盘通常已写入），
// 故该链路对已删渠道基本闭合（最终一致；极短窗口内可能短暂取不到名）。
func ResolveChannelDisplayNames(ids []int) map[int]string {
	result := ResolveChannelDisplayNamesWithoutLogs(ids)
	still := make([]int, 0)
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		if result[id] == "" {
			still = append(still, id)
		}
	}
	if len(still) > 0 {
		for id, name := range GetChannelNameSnapshotsFromLogs(still) {
			result[id] = name
		}
	}
	return result
}

// ChannelDisplayOption 渠道筛选下拉选项（含已删除渠道）。
type ChannelDisplayOption struct {
	ChannelId   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Deleted     bool   `json:"deleted"`
}

// ListChannelDisplayOptions 列出产生过消费的渠道（含已删除），用于筛选下拉。
// 数据源为 platform_channel_daily_stats（每渠道每天一行的小表，渠道删除时
// 渠道删除后 finalizeChannelDeletion 异步把名称快照回填），在世渠道叠加当前名称。
// 不扫描 logs / consumption_costs / employee_commission_logs 等大表。
func ListChannelDisplayOptions(page, pageSize int) ([]ChannelDisplayOption, int64, error) {
	var total int64
	if err := DB.Model(&PlatformChannelDailyStat{}).
		Distinct("channel_id").
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []struct {
		ChannelId   int
		ChannelName string
	}
	if err := DB.Model(&PlatformChannelDailyStat{}).
		Select("channel_id, MAX(channel_name) as channel_name").
		Group("channel_id").
		Order("channel_id").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ChannelId)
	}
	liveNames, err := GetChannelNamesByIds(ids)
	if err != nil {
		return nil, 0, err
	}
	options := make([]ChannelDisplayOption, 0, len(rows))
	for _, row := range rows {
		opt := ChannelDisplayOption{
			ChannelId:   row.ChannelId,
			ChannelName: row.ChannelName,
		}
		if liveName, ok := liveNames[row.ChannelId]; ok {
			if liveName != "" {
				opt.ChannelName = liveName
			}
		} else {
			opt.Deleted = true
		}
		options = append(options, opt)
	}
	return options, total, nil
}

func BatchSetChannelTag(ids []int, tag *string) error {
	// 开启事务
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// 更新标签
	err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("tag", tag).Error
	if err != nil {
		tx.Rollback()
		return err
	}

	// update ability status
	channels, err := GetChannelsByIds(ids)
	if err != nil {
		tx.Rollback()
		return err
	}

	for _, channel := range channels {
		err = channel.UpdateAbilities(tx)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	// 提交事务
	return tx.Commit().Error
}

// CountAllChannels returns total channels in DB
func CountAllChannels() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Count(&total).Error
	return total, err
}

// CountAllTags returns number of non-empty distinct tags
func CountAllTags() (int64, error) {
	return CountChannelTags(DB.Model(&Channel{}))
}

func CountChannelTags(query *gorm.DB) (int64, error) {
	var total int64
	err := query.Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// Get channels of specified type with pagination
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// Count channels of specific type
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// Return map[type]count for all channels
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	err := DB.Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}

// GetChannelsByGroup returns all channels belonging to a specific group
func GetChannelsByGroup(userGroup string) ([]*Channel, error) {
	var channels []*Channel
	userGroup = strings.TrimSpace(userGroup)
	if userGroup == "" {
		return channels, nil
	}
	err := DB.Where(channelGroupFilterCondition(), channelGroupFilterPattern(userGroup)).Find(&channels).Error
	return channels, err
}
