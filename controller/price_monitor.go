package controller

import (
	"crypto/subtle"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

const (
	priceSourceChannel                  = "channel"
	priceSourceOfficial                 = "official"
	priceSourceModelsDev                = "models_dev"
	priceMonitorPlatformKey             = "platform"
	priceMonitorModeToken               = "per_token"
	priceMonitorModeRequest             = "per_request"
	priceMonitorModeExpression          = "tiered_expr"
	priceMonitorMatrixVersion           = 12
	priceMonitorUnavailableMissing      = "missing"
	priceMonitorUnavailablePlaceholder  = "placeholder"
	priceMonitorUnavailableSourceFailed = "source_failed"
	priceMonitorLaneCacheRead           = "cache_read"
	priceMonitorLaneCacheWrite          = "cache_write"
	priceMonitorLaneCacheWrite1h        = "cache_write_1h"
	priceMonitorLaneImageInput          = "image_input"
	priceMonitorLaneImageOutput         = "image_output"
	priceMonitorLaneAudioInput          = "audio_input"
	priceMonitorLaneAudioOutput         = "audio_output"
)

type PriceMonitorSource struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value"`
	Type  string      `json:"source_type,omitempty"`
}

type PriceMonitorItem struct {
	Model    string               `json:"model"`
	Field    string               `json:"field"`
	Platform interface{}          `json:"platform"`
	Sources  []PriceMonitorSource `json:"sources"`
}

type PriceMonitorSourceHeader struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	APIURL string `json:"api_url,omitempty"`
}

type PriceMonitorPriceCell struct {
	Mode              string                  `json:"mode"`
	Input             *float64                `json:"input,omitempty"`
	Output            *float64                `json:"output,omitempty"`
	Price             *float64                `json:"price,omitempty"`
	Lanes             []PriceMonitorPriceLane `json:"lanes,omitempty"`
	Tiers             []PriceMonitorPriceTier `json:"tiers,omitempty"`
	Dynamic           bool                    `json:"dynamic,omitempty"`
	Different         bool                    `json:"different,omitempty"`
	InputDifferent    bool                    `json:"input_different,omitempty"`
	OutputDifferent   bool                    `json:"output_different,omitempty"`
	PriceDifferent    bool                    `json:"price_different,omitempty"`
	ModeDifferent     bool                    `json:"mode_different,omitempty"`
	UnavailableReason string                  `json:"unavailable_reason,omitempty"`
	Expr              string                  `json:"-"`
}

type PriceMonitorPriceTier struct {
	Range             string                  `json:"range"`
	ConditionVariable string                  `json:"condition_variable,omitempty"`
	ConditionOperator string                  `json:"condition_operator,omitempty"`
	ConditionValue    *float64                `json:"condition_value,omitempty"`
	Input             float64                 `json:"input"`
	Output            float64                 `json:"output"`
	Lanes             []PriceMonitorPriceLane `json:"lanes,omitempty"`
}

type PriceMonitorPriceLane struct {
	Key       string   `json:"key"`
	Price     *float64 `json:"price,omitempty"`
	Different bool     `json:"different,omitempty"`
}

type PriceMonitorMatrixItem struct {
	Model  string                           `json:"model"`
	Prices map[string]PriceMonitorPriceCell `json:"prices"`
}

type PriceMonitorComparisonModelCounts struct {
	PlatformOfficial  int `json:"platform_official"`
	PlatformModelsDev int `json:"platform_models_dev"`
	PlatformChannel   int `json:"platform_channel"`
	ChannelOfficial   int `json:"channel_official"`
	ChannelModelsDev  int `json:"channel_models_dev"`
}

type PriceMonitorSnapshot struct {
	CheckedAt             int64                             `json:"checked_at"`
	Status                string                            `json:"status"`
	SourceTotal           int                               `json:"source_total"`
	SourceOK              int                               `json:"source_ok"`
	SourceError           int                               `json:"source_err"`
	ModelCount            int                               `json:"model_count"`
	ItemCount             int                               `json:"item_count"`
	ComparisonModelCounts PriceMonitorComparisonModelCounts `json:"comparison_model_counts"`
	AccessPassword        string                            `json:"access_password"`
	PasswordExpireAt      int64                             `json:"password_expire_at"`
	Message               string                            `json:"message,omitempty"`
	Items                 []PriceMonitorItem                `json:"items"`
	SourceHeaders         []PriceMonitorSourceHeader        `json:"source_headers"`
	MatrixItems           []PriceMonitorMatrixItem          `json:"matrix_items"`
	MatrixVersion         int                               `json:"matrix_version"`
}

type priceMonitorSnapshotStore struct {
	path  string
	write sync.Mutex
	value atomic.Pointer[PriceMonitorSnapshot]
}

func newPriceMonitorSnapshotStore(path string) *priceMonitorSnapshotStore {
	store := &priceMonitorSnapshotStore{path: path}
	store.value.Store(&PriceMonitorSnapshot{Items: make([]PriceMonitorItem, 0)})
	return store
}

func (store *priceMonitorSnapshotStore) Load() error {
	data, err := os.ReadFile(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var snapshot PriceMonitorSnapshot
	if err := common.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	if snapshot.Items == nil {
		snapshot.Items = make([]PriceMonitorItem, 0)
	}
	if snapshot.SourceHeaders == nil {
		snapshot.SourceHeaders = make([]PriceMonitorSourceHeader, 0)
	}
	if snapshot.MatrixItems == nil {
		snapshot.MatrixItems = make([]PriceMonitorMatrixItem, 0)
	}
	store.value.Store(&snapshot)
	return nil
}

func (store *priceMonitorSnapshotStore) Save(snapshot PriceMonitorSnapshot) error {
	if snapshot.Items == nil {
		snapshot.Items = make([]PriceMonitorItem, 0)
	}
	if snapshot.SourceHeaders == nil {
		snapshot.SourceHeaders = make([]PriceMonitorSourceHeader, 0)
	}
	if snapshot.MatrixItems == nil {
		snapshot.MatrixItems = make([]PriceMonitorMatrixItem, 0)
	}
	data, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	store.write.Lock()
	defer store.write.Unlock()
	if err := writePriceMonitorFile(store.path, data); err != nil {
		return err
	}
	store.value.Store(&snapshot)
	return nil
}

func (store *priceMonitorSnapshotStore) Get() PriceMonitorSnapshot {
	snapshot := store.value.Load()
	if snapshot == nil {
		return PriceMonitorSnapshot{Items: make([]PriceMonitorItem, 0)}
	}
	return *snapshot
}

func writePriceMonitorFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func priceMonitorSnapshotPath() string {
	basePath := strings.TrimSpace(common.GetDiskCachePath())
	if basePath == "" {
		basePath = filepath.Join("data")
	}
	return filepath.Join(basePath, "price_monitor", "snapshot.json")
}

type priceMonitorQuery struct {
	Model         string
	Field         string
	Source        string
	SourceKeys    []string
	SourceKeysSet bool
	Comparison    string
	Page          int
	PageSize      int
}

type priceMonitorQueryResult struct {
	CheckedAt        int64              `json:"checked_at"`
	PasswordExpireAt int64              `json:"password_expire_at"`
	Status           string             `json:"status"`
	Total            int                `json:"total"`
	ModelTotal       int                `json:"model_total"`
	Page             int                `json:"page"`
	PageSize         int                `json:"page_size"`
	Items            []PriceMonitorItem `json:"items"`
}

type priceMonitorMatrixQueryResult struct {
	CheckedAt              int64                      `json:"checked_at"`
	PasswordExpireAt       int64                      `json:"password_expire_at"`
	Status                 string                     `json:"status"`
	Total                  int                        `json:"total"`
	Page                   int                        `json:"page"`
	PageSize               int                        `json:"page_size"`
	SourceHeaders          []PriceMonitorSourceHeader `json:"source_headers"`
	AvailableSourceHeaders []PriceMonitorSourceHeader `json:"available_source_headers"`
	AvailableModels        []string                   `json:"available_models"`
	AppliedFilters         priceMonitorAppliedFilters `json:"applied_filters"`
	Items                  []PriceMonitorMatrixItem   `json:"items"`
}

type priceMonitorAppliedFilters struct {
	SourceKeys []string `json:"source_keys"`
	Comparison string   `json:"comparison"`
}

func queryPriceMonitorMatrix(snapshot PriceMonitorSnapshot, query priceMonitorQuery) priceMonitorMatrixQueryResult {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 200 {
		query.PageSize = 200
	}
	modelFilter := strings.ToLower(strings.TrimSpace(query.Model))
	sourceFilter := strings.ToLower(strings.TrimSpace(query.Source))
	comparison := strings.ToLower(strings.TrimSpace(query.Comparison))
	switch comparison {
	case "channel_official", "channel_models_dev", "channel_platform", "platform_official", "platform_models_dev", "input", "output", "cache", "billing", "official_missing", "models_dev_missing", "channel_missing", "source_failed":
	default:
		comparison = "all"
	}
	selectedKeys := make(map[string]struct{}, len(query.SourceKeys))
	for _, key := range query.SourceKeys {
		key = strings.TrimSpace(key)
		if key != "" {
			selectedKeys[key] = struct{}{}
		}
	}
	eligibleHeaders := make([]PriceMonitorSourceHeader, 0, len(snapshot.SourceHeaders))
	for _, header := range snapshot.SourceHeaders {
		if header.Type == priceSourceChannel {
			if query.SourceKeysSet {
				if _, selected := selectedKeys[header.Key]; !selected {
					continue
				}
			} else if sourceFilter != "" && strings.ToLower(header.Type) != sourceFilter && !strings.Contains(strings.ToLower(header.Name), sourceFilter) && !strings.Contains(strings.ToLower(header.APIURL), sourceFilter) {
				continue
			}
		}
		eligibleHeaders = append(eligibleHeaders, header)
	}
	availableModels := make([]string, 0, len(snapshot.MatrixItems))
	for _, item := range snapshot.MatrixItems {
		availableModels = append(availableModels, item.Model)
	}
	sort.Strings(availableModels)
	filtered := make([]PriceMonitorMatrixItem, 0, len(snapshot.MatrixItems))
	visibleKeys := make(map[string]struct{})
	for _, item := range snapshot.MatrixItems {
		if modelFilter != "" && !strings.HasPrefix(strings.ToLower(item.Model), modelFilter) {
			continue
		}
		matched, displayKeys := priceMonitorMatrixItemDisplayKeys(item, eligibleHeaders, comparison)
		if !matched {
			continue
		}
		prices := make(map[string]PriceMonitorPriceCell, len(displayKeys))
		for key := range displayKeys {
			if price, ok := item.Prices[key]; ok {
				prices[key] = price
				visibleKeys[key] = struct{}{}
			}
		}
		filtered = append(filtered, PriceMonitorMatrixItem{Model: item.Model, Prices: prices})
	}
	start := (query.Page - 1) * query.PageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + query.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	pageItems := filtered[start:end]
	headers := make([]PriceMonitorSourceHeader, 0, len(eligibleHeaders))
	for _, header := range eligibleHeaders {
		if _, visible := visibleKeys[header.Key]; visible {
			headers = append(headers, header)
		}
	}
	availableHeaders := append([]PriceMonitorSourceHeader(nil), snapshot.SourceHeaders...)
	appliedKeys := make([]string, 0, len(selectedKeys))
	for _, header := range snapshot.SourceHeaders {
		if _, selected := selectedKeys[header.Key]; selected && header.Type == priceSourceChannel {
			appliedKeys = append(appliedKeys, header.Key)
		}
	}
	return priceMonitorMatrixQueryResult{CheckedAt: snapshot.CheckedAt, PasswordExpireAt: snapshot.PasswordExpireAt, Status: snapshot.Status, Total: len(filtered), Page: query.Page, PageSize: query.PageSize, SourceHeaders: headers, AvailableSourceHeaders: availableHeaders, AvailableModels: availableModels, AppliedFilters: priceMonitorAppliedFilters{SourceKeys: appliedKeys, Comparison: comparison}, Items: pageItems}
}

func priceMonitorMatrixItemDisplayKeys(item PriceMonitorMatrixItem, headers []PriceMonitorSourceHeader, comparison string) (bool, map[string]struct{}) {
	displayKeys := make(map[string]struct{})
	if comparison == "all" {
		hasComparableDifference := false
		matchingReferenceKeys := make([]string, 0, 2)
		for _, header := range headers {
			cell, ok := item.Prices[header.Key]
			if !ok || header.Type == priceMonitorPlatformKey {
				continue
			}
			if (header.Type == priceSourceOfficial || header.Type == priceSourceModelsDev) && cell.UnavailableReason == "" && !cell.Different {
				matchingReferenceKeys = append(matchingReferenceKeys, header.Key)
			}
			if priceMonitorCellHasComparableDifference(cell) {
				displayKeys[header.Key] = struct{}{}
				hasComparableDifference = true
			}
		}
		if hasComparableDifference {
			displayKeys[priceMonitorPlatformKey] = struct{}{}
			for _, key := range matchingReferenceKeys {
				displayKeys[key] = struct{}{}
			}
		}
		return hasComparableDifference, displayKeys
	}
	var official *PriceMonitorPriceCell
	officialKey := ""
	var modelsDev *PriceMonitorPriceCell
	modelsDevKey := ""
	for _, header := range headers {
		cell, ok := item.Prices[header.Key]
		if !ok {
			continue
		}
		switch header.Type {
		case priceSourceOfficial:
			copy := cell
			official = &copy
			officialKey = header.Key
		case priceSourceModelsDev:
			copy := cell
			modelsDev = &copy
			modelsDevKey = header.Key
		}
	}
	switch comparison {
	case "channel_official", "channel_models_dev":
		reference, referenceKey := official, officialKey
		if comparison == "channel_models_dev" {
			reference, referenceKey = modelsDev, modelsDevKey
		}
		if reference == nil || priceMonitorComparisonIgnores(*reference) {
			return false, displayKeys
		}
		for _, header := range headers {
			channel, ok := item.Prices[header.Key]
			if header.Type == priceSourceChannel && ok && !priceMonitorComparisonIgnores(channel) && !priceMonitorPriceCellsEqual(*reference, channel) {
				displayKeys[referenceKey] = struct{}{}
				displayKeys[header.Key] = struct{}{}
			}
		}
	case "channel_platform":
		for _, header := range headers {
			channel, ok := item.Prices[header.Key]
			if header.Type == priceSourceChannel && ok && !priceMonitorComparisonIgnores(channel) && channel.Different {
				displayKeys[priceMonitorPlatformKey] = struct{}{}
				displayKeys[header.Key] = struct{}{}
			}
		}
	case "platform_official", "platform_models_dev":
		reference, referenceKey := official, officialKey
		if comparison == "platform_models_dev" {
			reference, referenceKey = modelsDev, modelsDevKey
		}
		if reference != nil && !priceMonitorComparisonIgnores(*reference) && reference.Different {
			displayKeys[priceMonitorPlatformKey] = struct{}{}
			displayKeys[referenceKey] = struct{}{}
		}
	case "input":
		for _, header := range headers {
			source, ok := item.Prices[header.Key]
			if header.Type != priceMonitorPlatformKey && ok && !priceMonitorComparisonIgnores(source) && source.InputDifferent {
				displayKeys[priceMonitorPlatformKey] = struct{}{}
				displayKeys[header.Key] = struct{}{}
			}
		}
	case "output":
		for _, header := range headers {
			source, ok := item.Prices[header.Key]
			if header.Type != priceMonitorPlatformKey && ok && !priceMonitorComparisonIgnores(source) && source.OutputDifferent {
				displayKeys[priceMonitorPlatformKey] = struct{}{}
				displayKeys[header.Key] = struct{}{}
			}
		}
	case "cache":
		for _, header := range headers {
			source, ok := item.Prices[header.Key]
			if header.Type == priceMonitorPlatformKey || !ok || priceMonitorComparisonIgnores(source) {
				continue
			}
			for _, lane := range source.Lanes {
				if lane.Different && (lane.Key == priceMonitorLaneCacheRead || lane.Key == priceMonitorLaneCacheWrite || lane.Key == priceMonitorLaneCacheWrite1h) {
					displayKeys[priceMonitorPlatformKey] = struct{}{}
					displayKeys[header.Key] = struct{}{}
					break
				}
			}
		}
	case "billing":
		for _, header := range headers {
			source, ok := item.Prices[header.Key]
			if header.Type != priceMonitorPlatformKey && ok && !priceMonitorComparisonIgnores(source) && (source.ModeDifferent || source.PriceDifferent) {
				displayKeys[priceMonitorPlatformKey] = struct{}{}
				displayKeys[header.Key] = struct{}{}
			}
		}
	case "official_missing", "models_dev_missing":
		reference, referenceKey := official, officialKey
		if comparison == "models_dev_missing" {
			reference, referenceKey = modelsDev, modelsDevKey
		}
		if reference != nil && reference.UnavailableReason == priceMonitorUnavailableMissing {
			displayKeys[referenceKey] = struct{}{}
		}
	case "channel_missing":
		for _, header := range headers {
			channel, ok := item.Prices[header.Key]
			if header.Type == priceSourceChannel && ok && channel.UnavailableReason == priceMonitorUnavailableMissing {
				displayKeys[header.Key] = struct{}{}
			}
		}
	case "source_failed":
		for _, header := range headers {
			source, ok := item.Prices[header.Key]
			if header.Type != priceMonitorPlatformKey && ok && source.UnavailableReason == priceMonitorUnavailableSourceFailed {
				displayKeys[header.Key] = struct{}{}
			}
		}
	}
	return len(displayKeys) > 0, displayKeys
}

func countPriceMonitorComparisonModels(headers []PriceMonitorSourceHeader, items []PriceMonitorMatrixItem) PriceMonitorComparisonModelCounts {
	counts := PriceMonitorComparisonModelCounts{}
	for _, item := range items {
		if matched, _ := priceMonitorMatrixItemDisplayKeys(item, headers, "platform_official"); matched {
			counts.PlatformOfficial++
		}
		if matched, _ := priceMonitorMatrixItemDisplayKeys(item, headers, "platform_models_dev"); matched {
			counts.PlatformModelsDev++
		}
		if matched, _ := priceMonitorMatrixItemDisplayKeys(item, headers, "channel_platform"); matched {
			counts.PlatformChannel++
		}
		if matched, _ := priceMonitorMatrixItemDisplayKeys(item, headers, "channel_official"); matched {
			counts.ChannelOfficial++
		}
		if matched, _ := priceMonitorMatrixItemDisplayKeys(item, headers, "channel_models_dev"); matched {
			counts.ChannelModelsDev++
		}
	}
	return counts
}

func priceMonitorComparisonIgnores(cell PriceMonitorPriceCell) bool {
	return cell.UnavailableReason != ""
}

func priceMonitorCellHasComparableDifference(cell PriceMonitorPriceCell) bool {
	if priceMonitorComparisonIgnores(cell) {
		return false
	}
	return cell.Different
}

func priceMonitorPriceCellsEqual(left, right PriceMonitorPriceCell) bool {
	if left.UnavailableReason != "" || right.UnavailableReason != "" {
		return left.UnavailableReason == right.UnavailableReason
	}
	if left.Mode != right.Mode || left.Dynamic != right.Dynamic || !priceMonitorOptionalPriceEqual(left.Input, right.Input) || !priceMonitorOptionalPriceEqual(left.Output, right.Output) || !priceMonitorOptionalPriceEqual(left.Price, right.Price) || len(left.Lanes) != len(right.Lanes) || len(left.Tiers) != len(right.Tiers) {
		return false
	}
	if !priceMonitorLanePricesEqual(left.Lanes, right.Lanes) {
		return false
	}
	for i := range left.Tiers {
		leftTier, rightTier := left.Tiers[i], right.Tiers[i]
		if leftTier.Range != rightTier.Range || leftTier.ConditionVariable != rightTier.ConditionVariable || leftTier.ConditionOperator != rightTier.ConditionOperator || !priceMonitorOptionalPriceEqual(leftTier.ConditionValue, rightTier.ConditionValue) || !nearlyEqual(leftTier.Input, rightTier.Input) || !nearlyEqual(leftTier.Output, rightTier.Output) || len(leftTier.Lanes) != len(rightTier.Lanes) {
			return false
		}
		if !priceMonitorLanePricesEqual(leftTier.Lanes, rightTier.Lanes) {
			return false
		}
	}
	return true
}

func priceMonitorLanePricesEqual(left, right []PriceMonitorPriceLane) bool {
	if len(left) != len(right) {
		return false
	}
	rightByKey := make(map[string]*float64, len(right))
	for _, lane := range right {
		if _, exists := rightByKey[lane.Key]; exists {
			return false
		}
		rightByKey[lane.Key] = lane.Price
	}
	for _, lane := range left {
		rightPrice, ok := rightByKey[lane.Key]
		if !ok || !priceMonitorOptionalPriceEqual(lane.Price, rightPrice) {
			return false
		}
	}
	return true
}

func priceMonitorOptionalPriceEqual(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return nearlyEqual(*left, *right)
}

func queryPriceMonitorSnapshot(snapshot PriceMonitorSnapshot, query priceMonitorQuery) priceMonitorQueryResult {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 200 {
		query.PageSize = 200
	}
	modelFilter := strings.ToLower(strings.TrimSpace(query.Model))
	fieldFilter := strings.ToLower(strings.TrimSpace(query.Field))
	sourceFilter := strings.ToLower(strings.TrimSpace(query.Source))
	filtered := make([]PriceMonitorItem, 0)
	models := make(map[string]struct{})
	for _, item := range snapshot.Items {
		if modelFilter != "" && !strings.HasPrefix(strings.ToLower(item.Model), modelFilter) {
			continue
		}
		if fieldFilter != "" && strings.ToLower(item.Field) != fieldFilter {
			continue
		}
		matchedSources := make([]PriceMonitorSource, 0, len(item.Sources))
		for _, source := range item.Sources {
			if sourceFilter != "" && strings.ToLower(source.Type) != sourceFilter && !strings.Contains(strings.ToLower(source.Name), sourceFilter) {
				continue
			}
			source.Type = ""
			matchedSources = append(matchedSources, source)
		}
		if sourceFilter != "" && len(matchedSources) == 0 {
			continue
		}
		item.Sources = matchedSources
		filtered = append(filtered, item)
		models[item.Model] = struct{}{}
	}
	start := (query.Page - 1) * query.PageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + query.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	return priceMonitorQueryResult{
		CheckedAt:        snapshot.CheckedAt,
		PasswordExpireAt: snapshot.PasswordExpireAt,
		Status:           snapshot.Status,
		Total:            len(filtered),
		ModelTotal:       len(models),
		Page:             query.Page,
		PageSize:         query.PageSize,
		Items:            filtered[start:end],
	}
}

func buildPriceMonitorItems(differences map[string]map[string]dto.DifferenceItem, marketplaceModels map[string]struct{}, sourceTypes map[string]string, sourceModels map[string]map[string]struct{}) []PriceMonitorItem {
	items := make([]PriceMonitorItem, 0)
	for modelName, fields := range differences {
		if _, visible := marketplaceModels[modelName]; !visible {
			continue
		}
		for field, difference := range fields {
			sources := make([]PriceMonitorSource, 0)
			for sourceName, value := range difference.Upstreams {
				if sourceTypes[sourceName] == priceSourceChannel {
					if enabledModels, ok := sourceModels[sourceName]; ok {
						if _, enabled := enabledModels[modelName]; !enabled {
							continue
						}
					}
				}
				if value == nil || value == "same" || !difference.Confidence[sourceName] {
					continue
				}
				sources = append(sources, PriceMonitorSource{Name: sourceName, Value: value, Type: sourceTypes[sourceName]})
			}
			if len(sources) == 0 {
				continue
			}
			sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
			items = append(items, PriceMonitorItem{Model: modelName, Field: field, Platform: difference.Current, Sources: sources})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Model == items[j].Model {
			return items[i].Field < items[j].Field
		}
		return items[i].Model < items[j].Model
	})
	return items
}

func buildPriceMonitorMatrix(localData map[string]any, sources []pricingSource, marketplaceModels map[string]struct{}, sourceTypes map[string]string) ([]PriceMonitorSourceHeader, []PriceMonitorMatrixItem) {
	sort.Slice(sources, func(i, j int) bool {
		leftType, rightType := sourceTypes[sources[i].name], sourceTypes[sources[j].name]
		if leftType != rightType {
			leftRank, rightRank := 2, 2
			if leftType == priceSourceOfficial {
				leftRank = 0
			} else if leftType == priceSourceModelsDev {
				leftRank = 1
			}
			if rightType == priceSourceOfficial {
				rightRank = 0
			} else if rightType == priceSourceModelsDev {
				rightRank = 1
			}
			return leftRank < rightRank
		}
		return sources[i].name < sources[j].name
	})
	headers := buildPriceMonitorSourceHeaders(sources, sourceTypes)
	models := make([]string, 0, len(marketplaceModels))
	for modelName := range marketplaceModels {
		models = append(models, modelName)
	}
	sort.Strings(models)
	items := make([]PriceMonitorMatrixItem, 0)
	for _, modelName := range models {
		platform, platformOK := priceMonitorCell(localData, modelName)
		if !platformOK {
			continue
		}
		prices := map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform}
		hasDifference := false
		for _, source := range sources {
			if source.applicableModels != nil {
				if _, applicable := source.applicableModels[modelName]; !applicable {
					continue
				}
			}
			if source.failed {
				prices[source.name] = PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableSourceFailed}
				continue
			}
			cell, ok := priceMonitorCell(source.data, modelName)
			if !ok {
				prices[source.name] = PriceMonitorPriceCell{Different: true, UnavailableReason: priceMonitorUnavailableMissing}
				hasDifference = true
				continue
			}
			if isUntrustedPriceMonitorSource(source.data, modelName) {
				prices[source.name] = PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailablePlaceholder}
				continue
			}
			markPriceMonitorDifferences(platform, &cell)
			if cell.Different {
				hasDifference = true
			}
			prices[source.name] = cell
		}
		if hasDifference {
			items = append(items, PriceMonitorMatrixItem{Model: modelName, Prices: prices})
		}
	}
	return headers, items
}

func buildPriceMonitorSourceHeaders(sources []pricingSource, sourceTypes map[string]string) []PriceMonitorSourceHeader {
	headers := []PriceMonitorSourceHeader{{Key: priceMonitorPlatformKey, Name: "平台配置", Type: priceMonitorPlatformKey}}
	labelCounts := make(map[string]int)
	for _, source := range sources {
		sourceType := sourceTypes[source.name]
		label := priceMonitorSourceLabel(source.name, sourceType)
		labelCounts[label]++
		if labelCounts[label] > 1 {
			label += "（" + strconv.Itoa(labelCounts[label]) + "）"
		}
		headers = append(headers, PriceMonitorSourceHeader{Key: source.name, Name: label, Type: sourceType, APIURL: source.apiURL})
	}
	return headers
}

func priceMonitorDisplayURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

func priceMonitorSourceLabel(name, sourceType string) string {
	if sourceType == priceSourceOfficial {
		return "官方价格"
	}
	if sourceType == priceSourceModelsDev {
		return "models.dev 价格"
	}
	trimmed := strings.TrimSpace(name)
	open := strings.LastIndex(trimmed, "(")
	if open > 0 && strings.HasSuffix(trimmed, ")") {
		if _, err := strconv.Atoi(trimmed[open+1 : len(trimmed)-1]); err == nil {
			trimmed = strings.TrimSpace(trimmed[:open])
		}
	}
	return trimmed
}

func priceMonitorCell(data map[string]any, modelName string) (PriceMonitorPriceCell, bool) {
	mode, _ := valueMap(data[billing_setting.BillingModeField])[modelName].(string)
	expr, _ := valueMap(data[billing_setting.BillingExprField])[modelName].(string)
	if mode == billing_setting.BillingModeTieredExpr && strings.TrimSpace(expr) != "" {
		tiers, dynamic := parsePriceMonitorExpression(expr)
		return PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: strings.TrimSpace(expr), Tiers: tiers, Dynamic: dynamic}, true
	}
	if raw, ok := valueMap(data["model_price"])[modelName]; ok {
		if price, valid := asFloat64(raw); valid {
			return PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(price)}, true
		}
	}
	ratioRaw, ratioOK := valueMap(data["model_ratio"])[modelName]
	ratio, ratioValid := asFloat64(ratioRaw)
	if !ratioOK || !ratioValid {
		return PriceMonitorPriceCell{}, false
	}
	inputPrice := ratio * 2
	cell := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(inputPrice)}
	if completionRaw, completionOK := valueMap(data["completion_ratio"])[modelName]; completionOK {
		if completion, completionValid := asFloat64(completionRaw); completionValid {
			cell.Output = floatPointer(inputPrice * completion)
		}
	}
	for _, lane := range priceMonitorRatioLanes {
		raw, exists := valueMap(data[lane.field])[modelName]
		laneRatio, valid := asFloat64(raw)
		if !exists || !valid {
			continue
		}
		cell.Lanes = append(cell.Lanes, PriceMonitorPriceLane{Key: lane.key, Price: floatPointer(inputPrice * laneRatio)})
	}
	return cell, true
}

func floatPointer(value float64) *float64 { return &value }

var (
	priceMonitorTierPattern   = regexp.MustCompile(`tier\("[^"]*",\s*([^)]+)\)`)
	priceMonitorCoefficient   = regexp.MustCompile(`\b(p|c|cr|cc|cc1h|img|img_o|ai|ao)\s*\*\s*(-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)`)
	priceMonitorTierCondition = regexp.MustCompile(`(len|p|c)\s*(<=|<|>=|>)\s*(-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)\s*\?\s*$`)
	priceMonitorSafeTierBody  = regexp.MustCompile(`^\s*(?:(?:p|c|cr|cc|cc1h|img|img_o|ai|ao)\s*\*\s*-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)(?:\s*\+\s*(?:p|c|cr|cc|cc1h|img|img_o|ai|ao)\s*\*\s*-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)*\s*$`)
)

var priceMonitorRatioLanes = []struct {
	field string
	key   string
}{
	{field: "cache_ratio", key: priceMonitorLaneCacheRead},
	{field: "create_cache_ratio", key: priceMonitorLaneCacheWrite},
	{field: "image_ratio", key: priceMonitorLaneImageInput},
	{field: "audio_ratio", key: priceMonitorLaneAudioInput},
	{field: "audio_completion_ratio", key: priceMonitorLaneAudioOutput},
}

var priceMonitorExpressionLaneKeys = map[string]string{
	"cr":    priceMonitorLaneCacheRead,
	"cc":    priceMonitorLaneCacheWrite,
	"cc1h":  priceMonitorLaneCacheWrite1h,
	"img":   priceMonitorLaneImageInput,
	"img_o": priceMonitorLaneImageOutput,
	"ai":    priceMonitorLaneAudioInput,
	"ao":    priceMonitorLaneAudioOutput,
}

func parsePriceMonitorExpression(expression string) ([]PriceMonitorPriceTier, bool) {
	expression = strings.TrimSpace(expression)
	if expression == "" || strings.Contains(expression, "|||") || strings.Contains(expression, "header(") || strings.Contains(expression, "param(") || strings.Contains(expression, "hour(") || strings.Contains(expression, "minute(") || strings.Contains(expression, "weekday(") {
		return nil, true
	}
	matches := priceMonitorTierPattern.FindAllStringSubmatchIndex(expression, -1)
	if len(matches) == 0 || len(matches) > 2 {
		return nil, true
	}
	tiers := make([]PriceMonitorPriceTier, 0, len(matches))
	previousEnd := 0
	conditionVariable, conditionOperator, conditionValue := "", "", 0.0
	for index, match := range matches {
		body := expression[match[2]:match[3]]
		if !priceMonitorSafeTierBody.MatchString(body) {
			return nil, true
		}
		input, output := 0.0, 0.0
		lanes := make([]PriceMonitorPriceLane, 0)
		for _, coefficient := range priceMonitorCoefficient.FindAllStringSubmatch(body, -1) {
			value, err := strconv.ParseFloat(coefficient[2], 64)
			if err != nil {
				return nil, true
			}
			if coefficient[1] == "p" {
				input = value
			} else if coefficient[1] == "c" {
				output = value
			} else if key := priceMonitorExpressionLaneKeys[coefficient[1]]; key != "" {
				lanes = append(lanes, PriceMonitorPriceLane{Key: key, Price: floatPointer(value)})
			}
		}
		rangeLabel := "全部输入长度"
		tierConditionVariable, tierConditionOperator := "", ""
		var tierConditionValue *float64
		prefix := expression[previousEnd:match[0]]
		condition := priceMonitorTierCondition.FindStringSubmatch(prefix)
		if len(condition) > 0 {
			value, err := strconv.ParseFloat(condition[3], 64)
			if err != nil {
				return nil, true
			}
			conditionVariable, conditionOperator, conditionValue = condition[1], condition[2], value
			tierConditionVariable, tierConditionOperator, tierConditionValue = conditionVariable, conditionOperator, floatPointer(conditionValue)
			rangeLabel = priceMonitorConditionLabel(conditionVariable, conditionOperator, conditionValue)
		} else if index == 1 && conditionVariable != "" {
			tierConditionVariable, tierConditionOperator, tierConditionValue = conditionVariable, inversePriceMonitorOperator(conditionOperator), floatPointer(conditionValue)
			rangeLabel = priceMonitorConditionLabel(tierConditionVariable, tierConditionOperator, conditionValue)
		} else if len(matches) > 1 {
			return nil, true
		}
		tiers = append(tiers, PriceMonitorPriceTier{Range: rangeLabel, ConditionVariable: tierConditionVariable, ConditionOperator: tierConditionOperator, ConditionValue: tierConditionValue, Input: input, Output: output, Lanes: lanes})
		previousEnd = match[1]
	}
	return tiers, false
}

func priceMonitorConditionLabel(variable, operator string, value float64) string {
	name := "输入长度"
	if variable == "c" {
		name = "输出长度"
	}
	symbol := map[string]string{"<=": "≤", "<": "<", ">=": "≥", ">": ">"}[operator]
	formatted := strconv.FormatFloat(value, 'f', -1, 64)
	parts := strings.SplitN(formatted, ".", 2)
	for index := len(parts[0]) - 3; index > 0; index -= 3 {
		parts[0] = parts[0][:index] + "," + parts[0][index:]
	}
	return name + " " + symbol + " " + strings.Join(parts, ".") + " tokens"
}

func inversePriceMonitorOperator(operator string) string {
	return map[string]string{"<=": ">", "<": ">=", ">=": "<", ">": "<="}[operator]
}

func isUntrustedPriceMonitorSource(data map[string]any, modelName string) bool {
	ratio, ratioOK := asFloat64(valueMap(data["model_ratio"])[modelName])
	completion, completionOK := asFloat64(valueMap(data["completion_ratio"])[modelName])
	return ratioOK && completionOK && nearlyEqual(ratio, 37.5) && nearlyEqual(completion, 1)
}

func markPriceMonitorDifferences(platform PriceMonitorPriceCell, source *PriceMonitorPriceCell) {
	if platform.Mode != source.Mode {
		source.ModeDifferent = true
		source.Different = true
		return
	}
	switch platform.Mode {
	case priceMonitorModeToken:
		source.InputDifferent = priceMonitorValuesDifferent(platform.Input, source.Input)
		source.OutputDifferent = priceMonitorValuesDifferent(platform.Output, source.Output)
		laneDifferent := markPriceMonitorLaneDifferences(platform.Lanes, source)
		source.Different = source.InputDifferent || source.OutputDifferent || laneDifferent
	case priceMonitorModeRequest:
		source.PriceDifferent = platform.Price == nil || source.Price == nil || !nearlyEqual(*platform.Price, *source.Price)
		source.Different = source.PriceDifferent
	case priceMonitorModeExpression:
		if !platform.Dynamic && !source.Dynamic {
			source.Different = !priceMonitorPriceCellsEqual(platform, *source)
		} else {
			source.Different = platform.Expr != source.Expr
		}
		source.ModeDifferent = source.Different
	default:
		source.Different = true
		source.ModeDifferent = true
	}
}

func priceMonitorValuesDifferent(platform, source *float64) bool {
	if platform == nil || source == nil {
		return platform != nil || source != nil
	}
	return !nearlyEqual(*platform, *source)
}

func markPriceMonitorLaneDifferences(platformLanes []PriceMonitorPriceLane, source *PriceMonitorPriceCell) bool {
	platformByKey := make(map[string]PriceMonitorPriceLane, len(platformLanes))
	for _, lane := range platformLanes {
		platformByKey[lane.Key] = lane
	}
	sourceByKey := make(map[string]PriceMonitorPriceLane, len(source.Lanes))
	for _, lane := range source.Lanes {
		sourceByKey[lane.Key] = lane
	}
	ordered := make([]PriceMonitorPriceLane, 0, len(platformByKey)+len(sourceByKey))
	hasDifference := false
	for _, definition := range priceMonitorRatioLanes {
		platformLane, platformOK := platformByKey[definition.key]
		sourceLane, sourceOK := sourceByKey[definition.key]
		if !platformOK && !sourceOK {
			continue
		}
		if sourceOK {
			sourceLane.Different = !platformOK || platformLane.Price == nil || sourceLane.Price == nil || !nearlyEqual(*platformLane.Price, *sourceLane.Price)
			if sourceLane.Different {
				hasDifference = true
			}
			ordered = append(ordered, sourceLane)
			continue
		}
		ordered = append(ordered, PriceMonitorPriceLane{Key: definition.key})
	}
	source.Lanes = ordered
	return hasDifference
}

func validPriceMonitorPassword(snapshot PriceMonitorSnapshot, password string, now time.Time) bool {
	if snapshot.AccessPassword == "" || now.Unix() >= snapshot.PasswordExpireAt {
		return false
	}
	provided := []byte(password)
	expected := []byte(snapshot.AccessPassword)
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(provided, expected) == 1
}

func priceMonitorStorageScope() string {
	path := priceMonitorSnapshotPath()
	if priceMonitorStore != nil {
		path = priceMonitorStore.path
	}
	return fmt.Sprintf("local:%s", path)
}
