package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PricingValues map[string]any
type ModelPricingChange struct {
	ModelName       string        `json:"model_name"`
	ExpectedVersion string        `json:"expected_version"`
	Pricing         PricingValues `json:"pricing"`
	Reset           bool          `json:"reset,omitempty"`
}
type ModelPricingEntry struct {
	PricingVersion   int64         `json:"-"`
	ModelName        string        `json:"model_name"`
	Version          string        `json:"version"`
	Configured       PricingValues `json:"configured"`
	Effective        PricingValues `json:"effective"`
	CompletionLocked bool          `json:"completion_locked"`
}
type ModelPricingSnapshot struct {
	TokenPriceFactor float64             `json:"token_price_factor"`
	Entries          []ModelPricingEntry `json:"entries"`
	Options          map[string]string   `json:"options"`
	EmptyVersion     string              `json:"empty_version"`
	Capabilities     map[string]bool     `json:"capabilities"`
}

var ErrModelPricingConflict = errors.New("model pricing changed")

// This mutex only serializes administrator writes and their cache publication.
// Relay readers never acquire it.
var modelPricingMutationMu sync.Mutex
var modelPricingOptionKeys = []string{"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio", "CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio", "billing_setting.billing_expr", "billing_setting.billing_mode"}

func IsModelPricingOption(key string) bool {
	for _, candidate := range modelPricingOptionKeys {
		if key == candidate {
			return true
		}
	}
	return false
}
func ModelPricingVersion(values PricingValues) string {
	encoded, _ := common.Marshal(values)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func readModelPricingMaps(db *gorm.DB) (map[string]map[string]any, error) {
	var rows []Option
	keys := make([]any, 0, len(modelPricingOptionKeys))
	for _, key := range modelPricingOptionKeys {
		keys = append(keys, key)
	}
	if err := db.Where(clause.IN{Column: clause.Column{Name: "key"}, Values: keys}).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make(map[string]map[string]any, len(modelPricingOptionKeys))
	for _, row := range rows {
		if row.Value == "" {
			continue
		}
		var entries map[string]any
		if err := common.UnmarshalJsonStr(row.Value, &entries); err != nil {
			return nil, err
		}
		if entries == nil {
			return nil, fmt.Errorf("%s must be an object", row.Key)
		}
		values[row.Key] = entries
	}
	return values, nil
}

func modelPricingValues(values map[string]map[string]any, name string) PricingValues {
	result := PricingValues{}
	for _, key := range modelPricingOptionKeys {
		if value, ok := values[key][name]; ok {
			result[key] = value
		}
	}
	return result
}

func effectiveModelPricing(name string, configured PricingValues) PricingValues {
	result := PricingValues{}
	for key, value := range configured {
		result[key] = value
	}
	if result["billing_setting.billing_mode"] == billing_setting.BillingModeTieredExpr {
		return result
	}
	if _, fixed := result["ModelPrice"]; fixed {
		return result
	}
	if _, priced := result["ModelRatio"]; !priced {
		return result
	}
	var configuredCompletion *float64
	if value, ok := result["CompletionRatio"].(float64); ok {
		configuredCompletion = &value
	}
	completion := ratio_setting.ResolvePricingCompletionRatio(name, configuredCompletion)
	if _, ok := result["CompletionRatio"]; !ok || completion.Locked {
		result["CompletionRatio"] = completion.Ratio
	}
	for key, fallback := range map[string]float64{"CacheRatio": 1, "CreateCacheRatio": 1.25, "ImageRatio": 1, "AudioRatio": 1, "AudioCompletionRatio": 1} {
		if _, ok := result[key]; !ok {
			result[key] = fallback
		}
	}
	return result
}

func GetModelPricingSnapshot(names []string) (*ModelPricingSnapshot, error) {
	values, err := readModelPricingMaps(DB)
	if err != nil {
		return nil, err
	}
	defaults := ratio_setting.GetDefaultPricingMaps()
	if len(names) == 0 {
		unique := map[string]bool{}
		for key, entries := range defaults {
			if values[key] == nil {
				for name := range entries {
					unique[name] = true
				}
			}
		}
		for _, entries := range values {
			for name := range entries {
				unique[name] = true
			}
		}
		for name := range unique {
			names = append(names, name)
		}
	}
	names = append([]string(nil), names...)
	sort.Strings(names)
	result := &ModelPricingSnapshot{Entries: []ModelPricingEntry{}, Options: map[string]string{}, EmptyVersion: ModelPricingVersion(PricingValues{}), Capabilities: map[string]bool{"expression": true, "fixed": false, "image_count": false, "img_cr": false, "plugin_usage": false}}
	if common.QuotaPerUnit > 0 && !math.IsNaN(common.QuotaPerUnit) && !math.IsInf(common.QuotaPerUnit, 0) {
		result.TokenPriceFactor = 1_000_000 / common.QuotaPerUnit
	}
	for _, key := range modelPricingOptionKeys {
		entries := values[key]
		if entries == nil {
			entries = map[string]any{}
			for name, value := range defaults[key] {
				entries[name] = value
			}
		}
		encoded, err := common.Marshal(entries)
		if err != nil {
			return nil, err
		}
		result.Options[key] = string(encoded)
	}
	for _, name := range names {
		configured := modelPricingValues(values, name)
		effective := resolveModelPricingDraft(values, name, configured)
		var completion *float64
		if value, ok := effective["CompletionRatio"].(float64); ok {
			completion = &value
		}
		result.Entries = append(result.Entries, ModelPricingEntry{ModelName: name, Version: ModelPricingVersion(configured), Configured: configured, Effective: effective, CompletionLocked: ratio_setting.ResolvePricingCompletionRatio(name, completion).Locked})
	}
	return result, nil
}

func ValidateModelPricing(name string, draft PricingValues) error {
	if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || len(name) > 255 || draft == nil {
		return errors.New("invalid model pricing draft")
	}
	for key, value := range draft {
		if !IsModelPricingOption(key) {
			return fmt.Errorf("unsupported pricing field: %s", key)
		}
		switch key {
		case "billing_setting.billing_mode":
			if value != billing_setting.BillingModeRatio && value != billing_setting.BillingModeTieredExpr {
				return errors.New("invalid billing mode")
			}
		case "billing_setting.billing_expr":
			expression, ok := value.(string)
			if !ok || strings.TrimSpace(expression) == "" {
				return errors.New("billing expression is required")
			}
			program, err := billingexpr.CompilePreview(expression)
			if err != nil {
				return err
			}
			for _, tokens := range []float64{0, 1, 1000, 1_000_000} {
				params := billingexpr.TokenParams{P: tokens, C: tokens, Len: tokens, CR: tokens, CC: tokens, CC1h: tokens, Img: tokens, ImgO: tokens, AI: tokens, AO: tokens}
				if _, _, err := program.RunAt(params, billingexpr.RequestInput{}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
					return err
				}
			}
		default:
			number, ok := value.(float64)
			if !ok || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
				return fmt.Errorf("invalid price: %s", key)
			}
		}
	}
	if draft["billing_setting.billing_mode"] == billing_setting.BillingModeTieredExpr {
		if _, ok := draft["billing_setting.billing_expr"].(string); !ok {
			return errors.New("billing expression is required")
		}
	}
	return nil
}

func PreviewModelPricing(name string, draft PricingValues) (PricingValues, error) {
	if err := ValidateModelPricing(name, draft); err != nil {
		return nil, err
	}
	values, err := readModelPricingMaps(DB)
	if err != nil {
		return nil, err
	}
	return resolveModelPricingDraft(values, name, draft), nil
}

func resolveModelPricingDraft(values map[string]map[string]any, name string, draft PricingValues) PricingValues {
	resolved := PricingValues{}
	for key, value := range draft {
		resolved[key] = value
	}
	alias := ratio_setting.FormatMatchingModelName(name)
	for _, key := range modelPricingOptionKeys {
		lookup := name
		switch key {
		case "ModelRatio", "ModelPrice", "CompletionRatio", "AudioRatio", "AudioCompletionRatio":
			lookup = alias
		}
		if lookup == name {
			if _, explicit := draft[key]; explicit {
				continue
			}
			if values[key] != nil {
				continue
			}
		}
		entries := values[key]
		if lookup != name {
			delete(resolved, key)
		}
		if entries == nil {
			if value, ok := ratio_setting.GetDefaultPricingValue(key, lookup); ok {
				resolved[key] = value
			}
			continue
		}
		if value, ok := entries[lookup]; ok {
			resolved[key] = value
		}
	}
	return effectiveModelPricing(name, resolved)
}

func UpdateModelPricing(changes []ModelPricingChange) error {
	return UpdateModelPricingWithGuard(changes, nil)
}

func ResetModelPricingDraft(name string) PricingValues {
	draft := PricingValues{}
	for _, key := range modelPricingOptionKeys {
		if value, ok := ratio_setting.GetDefaultPricingValue(key, name); ok {
			draft[key] = value
		}
	}
	return draft
}

// The guard runs after version verification, before writes, with the exact drafts
// that will be committed. It must not perform external calls or database reads.
func UpdateModelPricingWithGuard(changes []ModelPricingChange, guard func([]ModelPricingEntry, []ModelPricingChange) error) error {
	if len(changes) == 0 || len(changes) > 100 {
		return errors.New("invalid pricing change count")
	}
	seen := map[string]bool{}
	changes = append([]ModelPricingChange(nil), changes...)
	for index, change := range changes {
		if seen[change.ModelName] {
			return errors.New("duplicate model pricing change")
		}
		seen[change.ModelName] = true
		if change.Reset {
			change.Pricing = ResetModelPricingDraft(change.ModelName)
			changes[index].Pricing = change.Pricing
		}
		if err := ValidateModelPricing(change.ModelName, change.Pricing); err != nil {
			return err
		}
	}
	modelPricingMutationMu.Lock()
	defer modelPricingMutationMu.Unlock()
	written := map[string]string{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		version, err := lockModelPricingVersion(tx)
		if err != nil {
			return err
		}
		values, err := readModelPricingMaps(lockedPricingTx(tx))
		if err != nil {
			return err
		}
		current := make([]ModelPricingEntry, 0, len(changes))
		for _, change := range changes {
			before := modelPricingValues(values, change.ModelName)
			if ModelPricingVersion(before) != change.ExpectedVersion {
				return ErrModelPricingConflict
			}
			if err := validateCompletionPricingChange(change.ModelName, before, change.Pricing); err != nil {
				return err
			}
			current = append(current, ModelPricingEntry{PricingVersion: version, ModelName: change.ModelName, Version: change.ExpectedVersion, Configured: before, Effective: resolveModelPricingDraft(values, change.ModelName, before)})
		}
		if guard != nil {
			if err := guard(current, changes); err != nil {
				return err
			}
		}
		changedKeys := map[string]bool{}
		for _, change := range changes {
			before := modelPricingValues(values, change.ModelName)
			if ModelPricingVersion(before) == ModelPricingVersion(change.Pricing) {
				continue
			}
			for _, key := range modelPricingOptionKeys {
				old, oldOK := values[key][change.ModelName]
				next, nextOK := change.Pricing[key]
				if oldOK == nextOK && old == next {
					continue
				}
				if values[key] == nil {
					values[key] = map[string]any{}
					for name, value := range ratio_setting.GetDefaultPricingMaps()[key] {
						values[key][name] = value
					}
				}
				delete(values[key], change.ModelName)
				if nextOK {
					values[key][change.ModelName] = next
				}
				changedKeys[key] = true
			}
		}
		for _, key := range modelPricingOptionKeys {
			if changedKeys[key] {
				encoded, err := common.Marshal(values[key])
				if err != nil {
					return err
				}
				written[key] = string(encoded)
			}
		}
		if len(written) == 0 {
			return nil
		}
		written[PricingConfigVersionKey] = strconv.FormatInt(version+1, 10)
		return persistModelPricingValues(tx, written)
	})
	if err != nil {
		return err
	}
	if err := publishModelPricingOptions(written); err != nil {
		return err
	}
	InvalidatePricingCache()
	ratio_setting.InvalidateExposedDataCache()
	return nil
}

// Create the shared version row before locking it. This also serializes the first
// write on PostgreSQL/MySQL; selecting a missing row alone cannot do that.
func lockModelPricingVersion(tx *gorm.DB) (int64, error) {
	row := Option{Key: PricingConfigVersionKey, Value: "0"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return 0, err
	}
	return readPricingVersionForUpdate(lockedPricingTx(tx))
}

func persistModelPricingValues(tx *gorm.DB, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		row := Option{Key: key, Value: values[key]}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// Legacy full-map saves use the same row lock, validation, version and ordered
// publication as the model-level API. Mixed non-pricing options remain atomic.
func updateModelPricingOptionsBulk(updates map[string]string) error {
	return updateModelPricingOptionMaps(updates, nil)
}

func validateCompletionPricingChange(name string, previous, draft PricingValues) error {
	if draft["billing_setting.billing_mode"] == billing_setting.BillingModeTieredExpr {
		return nil
	}
	value, exists := draft["CompletionRatio"].(float64)
	if !exists || previous["CompletionRatio"] == value {
		return nil
	}
	if _, configured := previous["CompletionRatio"]; !configured {
		if fallback, ok := ratio_setting.GetDefaultPricingValue("CompletionRatio", name); ok && fallback == value {
			return nil
		}
	}
	info := ratio_setting.ResolvePricingCompletionRatio(name, &value)
	if info.Locked && value != info.Ratio {
		return errors.New("completion pricing is fixed for this model")
	}
	return nil
}

// UpdateModelPricingOptionMaps is the legacy settings editor's atomic CAS API.
// It accepts only pricing maps, compares parsed JSON, and commits mode/expression
// together so no half-configured model can become active.
func UpdateModelPricingOptionMaps(updates, expected map[string]string) error {
	if len(updates) == 0 || len(updates) != len(expected) {
		return errors.New("expected pricing maps are required")
	}
	for key := range updates {
		if !IsModelPricingOption(key) {
			return errors.New("unsupported pricing map")
		}
		if _, ok := expected[key]; !ok {
			return errors.New("missing expected pricing map")
		}
	}
	return updateModelPricingOptionMaps(updates, expected)
}

func updateModelPricingOptionMaps(updates, expected map[string]string) error {
	modelPricingMutationMu.Lock()
	defer modelPricingMutationMu.Unlock()
	written := make(map[string]string, len(updates)+1)
	for key, value := range updates {
		written[key] = value
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		version, err := lockModelPricingVersion(tx)
		if err != nil {
			return err
		}
		values, err := readModelPricingMaps(lockedPricingTx(tx))
		if err != nil {
			return err
		}
		for key, raw := range expected {
			var prior map[string]any
			if err := common.UnmarshalJsonStr(raw, &prior); err != nil {
				return err
			}
			if prior == nil {
				return errors.New("expected pricing values must be an object")
			}
			current := values[key]
			if current == nil {
				current = map[string]any{}
				for name, value := range ratio_setting.GetDefaultPricingMaps()[key] {
					current[name] = value
				}
			}
			priorJSON, err := common.Marshal(prior)
			if err != nil {
				return err
			}
			currentJSON, err := common.Marshal(current)
			if err != nil {
				return err
			}
			if string(priorJSON) != string(currentJSON) {
				return ErrModelPricingConflict
			}
		}
		affected := map[string]bool{}
		previous := map[string]PricingValues{}
		for key, raw := range updates {
			if !IsModelPricingOption(key) {
				continue
			}
			var entries map[string]any
			if err := common.UnmarshalJsonStr(raw, &entries); err != nil {
				return err
			}
			if entries == nil {
				return errors.New("pricing values must be an object")
			}
			for name := range values[key] {
				affected[name] = true
				if _, ok := previous[name]; !ok {
					previous[name] = modelPricingValues(values, name)
				}
			}
			for name := range entries {
				affected[name] = true
				if _, ok := previous[name]; !ok {
					previous[name] = modelPricingValues(values, name)
				}
			}
			values[key] = entries
		}
		for name := range affected {
			if err := validateCompletionPricingChange(name, previous[name], modelPricingValues(values, name)); err != nil {
				return err
			}
			if err := ValidateModelPricing(name, modelPricingValues(values, name)); err != nil {
				return err
			}
		}
		written[PricingConfigVersionKey] = strconv.FormatInt(version+1, 10)
		return persistModelPricingValues(tx, written)
	})
	if err != nil {
		return err
	}
	if err := publishModelPricingOptions(written); err != nil {
		return err
	}
	InvalidatePricingCache()
	ratio_setting.InvalidateExposedDataCache()
	return nil
}
