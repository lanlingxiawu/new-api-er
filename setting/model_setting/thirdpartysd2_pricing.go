package model_setting

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
)

// ThirdPartySD2ResolutionPricing is the administrator override of one
// resolution tier. Both prices are pointers so an absent field keeps the
// built-in price while an explicit 0 stays a real free tier: with plain
// float64 fields, saving only `with_video` decoded `no_video` as 0 and
// overwrote the built-in price with a free tier, which made every request at
// that resolution free and booked negative revenue on settlement.
type ThirdPartySD2ResolutionPricing struct {
	NoVideo   *float64 `json:"no_video,omitempty"`
	WithVideo *float64 `json:"with_video,omitempty"`
}

// ThirdPartySD2PricingMatrix maps model -> resolution -> override.
//
// A nil (JSON null) resolution entry removes that resolution from the model.
// The matrix is the only availability switch a model's resolutions have
// (ValidateForkTaskUsageFacts rejects what the matrix does not price), so
// without the tombstone the set of accepted resolutions could only ever grow
// past the built-in defaults.
type ThirdPartySD2PricingMatrix map[string]map[string]*ThirdPartySD2ResolutionPricing

type ThirdPartySD2PricingSettings struct {
	Matrix ThirdPartySD2PricingMatrix `json:"matrix"`
}

// thirdPartySD2Prices is a resolved tier: both prices are known numbers.
type thirdPartySD2Prices struct {
	NoVideo   float64
	WithVideo float64
}

type thirdPartySD2ResolvedMatrix map[string]map[string]thirdPartySD2Prices

// thirdPartySD2UsageFactKeys are the plugin usage facts the generated
// expressions may read.
var thirdPartySD2UsageFactKeys = []string{"output_resolution", "tokens", "video_input"}

var defaultThirdPartySD2PricingMatrix = thirdPartySD2ResolvedMatrix{
	"dreamina-seedance-2-0-260128": {
		"480p": {
			NoVideo:   7.0,
			WithVideo: 4.3,
		},
		"720p": {
			NoVideo:   7.0,
			WithVideo: 4.3,
		},
		"1080p": {
			NoVideo:   7.7,
			WithVideo: 4.7,
		},
		"4k": {
			NoVideo:   4.0,
			WithVideo: 2.4,
		},
	},
	"dreamina-seedance-2-0-fast-260128": {
		"480p": {
			NoVideo:   5.6,
			WithVideo: 3.3,
		},
		"720p": {
			NoVideo:   5.6,
			WithVideo: 3.3,
		},
	},
}

var thirdPartySD2PricingSettings = ThirdPartySD2PricingSettings{
	Matrix: publicThirdPartySD2PricingMatrix(defaultThirdPartySD2PricingMatrix),
}

type thirdPartySD2PricingIndex struct {
	matrix thirdPartySD2ResolvedMatrix
	// billingExprs is the matrix rendered as one task usage expression per model.
	billingExprs map[string]string
}

var currentThirdPartySD2PricingIndex atomic.Pointer[thirdPartySD2PricingIndex]

// thirdpartysd2 prices are configured as $/1M tokens, so pre-consume uses a 1M-token estimate.
const ThirdPartySD2PreConsumedTokenEstimate = 1_000_000

func init() {
	config.GlobalConfig.Register("thirdpartysd2_pricing", &thirdPartySD2PricingSettings)
	RebuildThirdPartySD2PricingIndex()
}

func RebuildThirdPartySD2PricingIndex() {
	merged, problems := resolveThirdPartySD2PricingMatrix(thirdPartySD2PricingSettings.Matrix)
	for _, problem := range problems {
		// Save-time validation rejects these, so a problem here means the row
		// was written outside the settings API. The tier is dropped instead of
		// billed at a guessed price.
		common.SysError("thirdpartysd2 pricing matrix ignored: " + problem)
	}
	currentThirdPartySD2PricingIndex.Store(&thirdPartySD2PricingIndex{
		matrix:       merged,
		billingExprs: buildThirdPartySD2BillingExprs(merged),
	})
}

// ThirdPartySD2PluginKey is the task plugin that serves ThirdPartySD2 channels.
const ThirdPartySD2PluginKey = "thirdpartysd2"

// GetThirdPartySD2BillingExpr returns the pricing matrix of a model as a task
// usage expression over the thirdpartysd2 plugin facts tokens,
// output_resolution and video_input. Matrix prices are $/1M tokens, so the
// expression returns the task cost in USD. A model whose resolutions were all
// removed has no expression.
func GetThirdPartySD2BillingExpr(modelName string) (string, bool) {
	idx := currentThirdPartySD2PricingIndex.Load()
	if idx == nil {
		return "", false
	}
	expression, ok := idx.billingExprs[strings.TrimSpace(modelName)]
	return expression, ok
}

func buildThirdPartySD2BillingExprs(matrix thirdPartySD2ResolvedMatrix) map[string]string {
	expressions := make(map[string]string, len(matrix))
	for modelName, resolutions := range matrix {
		if expression := buildThirdPartySD2BillingExpr(resolutions); expression != "" {
			expressions[modelName] = expression
		}
	}
	return expressions
}

// buildThirdPartySD2BillingExpr renders one chained ternary with a tier per
// resolution and video-input combination, ordered by resolution rank. The
// plugin rejects resolutions a model does not price before billing, so the
// final else is the last listed tier instead of an unpriced fallback. A model
// with no priced resolution renders no expression at all.
func buildThirdPartySD2BillingExpr(resolutions map[string]thirdPartySD2Prices) string {
	keys := thirdPartySD2RankedResolutions(resolutions)
	if len(keys) == 0 {
		return ""
	}
	type branch struct{ condition, tier string }
	branches := make([]branch, 0, len(keys)*2)
	for _, resolution := range keys {
		pricing := resolutions[resolution]
		for _, variant := range thirdPartySD2Variants(resolution, pricing) {
			branches = append(branches, branch{
				condition: fmt.Sprintf(`u("output_resolution") == %q && u("video_input") == %q`, resolution, variant.videoInput),
				tier:      fmt.Sprintf(`tier(%q, u("tokens") * %s / 1000000)`, variant.tierName, strconv.FormatFloat(variant.price, 'f', -1, 64)),
			})
		}
	}
	var builder strings.Builder
	for _, item := range branches[:len(branches)-1] {
		builder.WriteString(item.condition)
		builder.WriteString(" ? ")
		builder.WriteString(item.tier)
		builder.WriteString(" : ")
	}
	builder.WriteString(branches[len(branches)-1].tier)
	return builder.String()
}

type thirdPartySD2Variant struct {
	videoInput string
	tierName   string
	price      float64
}

func thirdPartySD2Variants(resolution string, pricing thirdPartySD2Prices) []thirdPartySD2Variant {
	return []thirdPartySD2Variant{
		{videoInput: "none", tierName: resolution, price: pricing.NoVideo},
		{videoInput: "video", tierName: resolution + "_video", price: pricing.WithVideo},
	}
}

func thirdPartySD2RankedResolutions(resolutions map[string]thirdPartySD2Prices) []string {
	keys := make([]string, 0, len(resolutions))
	for resolution := range resolutions {
		if thirdPartySD2ResolutionRank(resolution) > 0 {
			keys = append(keys, resolution)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return thirdPartySD2ResolutionRank(keys[i]) < thirdPartySD2ResolutionRank(keys[j])
	})
	return keys
}

func GetThirdPartySD2PricingMatrix() ThirdPartySD2PricingMatrix {
	idx := currentThirdPartySD2PricingIndex.Load()
	if idx == nil || idx.matrix == nil {
		return publicThirdPartySD2PricingMatrix(defaultThirdPartySD2PricingMatrix)
	}
	return publicThirdPartySD2PricingMatrix(idx.matrix)
}

// GetThirdPartySD2Resolutions returns the resolutions priced for a model in the
// merged matrix, ordered by rank. A model accepts exactly these resolutions, so
// a model configured with none accepts nothing and reports an empty list.
func GetThirdPartySD2Resolutions(modelName string) ([]string, bool) {
	idx := currentThirdPartySD2PricingIndex.Load()
	if idx == nil || idx.matrix == nil {
		return nil, false
	}
	resolutions, ok := idx.matrix[strings.TrimSpace(modelName)]
	if !ok {
		return nil, false
	}
	return thirdPartySD2RankedResolutions(resolutions), true
}

func GetThirdPartySD2TokenPrice(modelName, resolution string, hasVideoInput bool) (float64, bool) {
	idx := currentThirdPartySD2PricingIndex.Load()
	if idx == nil || idx.matrix == nil {
		RebuildThirdPartySD2PricingIndex()
		idx = currentThirdPartySD2PricingIndex.Load()
		if idx == nil || idx.matrix == nil {
			return 0, false
		}
	}

	resolutions, ok := idx.matrix[strings.TrimSpace(modelName)]
	if !ok {
		return 0, false
	}
	pricing, ok := resolutions[NormalizeThirdPartySD2Resolution(resolution)]
	if !ok {
		return 0, false
	}
	if hasVideoInput {
		return pricing.WithVideo, true
	}
	return pricing.NoVideo, true
}

func NormalizeThirdPartySD2Resolution(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, " ", "")
	switch value {
	case "480p", "720p", "1080p", "4k":
		return value
	case "2160p":
		return "4k"
	}

	if strings.HasSuffix(value, "p") {
		height, err := strconv.Atoi(strings.TrimSuffix(value, "p"))
		if err == nil {
			return classifyThirdPartySD2PricingResolution(height)
		}
	}

	left, right, ok := strings.Cut(value, "x")
	if !ok {
		return ""
	}
	width, err := strconv.Atoi(strings.TrimSpace(left))
	if err != nil {
		return ""
	}
	height, err := strconv.Atoi(strings.TrimSpace(right))
	if err != nil {
		return ""
	}
	if width <= 0 || height <= 0 {
		return ""
	}
	if width < height {
		return classifyThirdPartySD2PricingResolution(width)
	}
	return classifyThirdPartySD2PricingResolution(height)
}

func MaxThirdPartySD2Resolution(values ...string) string {
	best := ""
	bestRank := -1
	for _, value := range values {
		normalized := NormalizeThirdPartySD2Resolution(value)
		rank := thirdPartySD2ResolutionRank(normalized)
		if rank > bestRank {
			best = normalized
			bestRank = rank
		}
	}
	return best
}

func ThirdPartySD2PriceToModelRatio(pricePerMillion float64) float64 {
	if pricePerMillion <= 0 {
		return 0
	}
	return pricePerMillion * common.QuotaPerUnit / 1_000_000
}

func CalculateThirdPartySD2Quota(pricePerMillion float64, tokenCount int, groupRatio float64) int {
	if pricePerMillion <= 0 || tokenCount <= 0 || groupRatio <= 0 {
		return 0
	}
	modelRatio := ThirdPartySD2PriceToModelRatio(pricePerMillion)
	return int(math.Round(float64(tokenCount) * modelRatio * groupRatio))
}

func ValidateThirdPartySD2PricingMatrixJSON(raw string) error {
	var matrix ThirdPartySD2PricingMatrix
	if err := common.UnmarshalJsonStr(raw, &matrix); err != nil {
		return err
	}
	return validateThirdPartySD2PricingMatrix(matrix)
}

func validateThirdPartySD2PricingMatrix(matrix ThirdPartySD2PricingMatrix) error {
	if matrix == nil {
		return fmt.Errorf("pricing matrix must be a JSON object")
	}

	for _, modelName := range slices.Sorted(maps.Keys(matrix)) {
		resolutions := matrix[modelName]
		normalizedModelName := strings.TrimSpace(modelName)
		if normalizedModelName == "" {
			return fmt.Errorf("model name cannot be empty")
		}
		if resolutions == nil {
			return fmt.Errorf("model %s must map to an object", modelName)
		}
		normalizedResolutions := make(map[string]struct{}, len(resolutions))
		for _, resolution := range slices.Sorted(maps.Keys(resolutions)) {
			pricing := resolutions[resolution]
			normalizedResolution := NormalizeThirdPartySD2Resolution(resolution)
			if normalizedResolution == "" {
				return fmt.Errorf("resolution cannot be empty for model %s", modelName)
			}
			if _, exists := normalizedResolutions[normalizedResolution]; exists {
				return fmt.Errorf("duplicate resolution %s after normalization for model %s", resolution, normalizedModelName)
			}
			normalizedResolutions[normalizedResolution] = struct{}{}
			if pricing == nil {
				// null removes the resolution from the model.
				continue
			}
			if err := validateThirdPartySD2Price(modelName, resolution, "no_video", pricing.NoVideo); err != nil {
				return err
			}
			if err := validateThirdPartySD2Price(modelName, resolution, "with_video", pricing.WithVideo); err != nil {
				return err
			}
		}
	}

	merged, problems := resolveThirdPartySD2PricingMatrix(matrix)
	if len(problems) > 0 {
		return errors.New(problems[0])
	}
	return smokeTestThirdPartySD2BillingExprs(merged)
}

func validateThirdPartySD2Price(modelName, resolution, field string, price *float64) error {
	if price == nil {
		return nil
	}
	if *price < 0 {
		return fmt.Errorf("price cannot be negative for %s %s %s", modelName, resolution, field)
	}
	if math.IsNaN(*price) || math.IsInf(*price, 0) {
		return fmt.Errorf("price must be a finite number for %s %s %s", modelName, resolution, field)
	}
	return nil
}

// resolveThirdPartySD2PricingMatrix merges the administrator overrides onto the
// built-in matrix field by field: an absent price keeps the built-in one, an
// explicit price (including 0) replaces it, and a null resolution removes the
// tier. Tiers that end up without a price are reported and left out so no
// request is ever billed at a guessed price.
func resolveThirdPartySD2PricingMatrix(overlay ThirdPartySD2PricingMatrix) (thirdPartySD2ResolvedMatrix, []string) {
	merged := cloneThirdPartySD2ResolvedMatrix(defaultThirdPartySD2PricingMatrix)
	var problems []string
	for _, modelName := range slices.Sorted(maps.Keys(overlay)) {
		normalizedModelName := strings.TrimSpace(modelName)
		if normalizedModelName == "" {
			continue
		}
		if merged[normalizedModelName] == nil {
			merged[normalizedModelName] = make(map[string]thirdPartySD2Prices, len(overlay[modelName]))
		}
		for _, resolution := range slices.Sorted(maps.Keys(overlay[modelName])) {
			normalizedResolution := NormalizeThirdPartySD2Resolution(resolution)
			if normalizedResolution == "" {
				continue
			}
			override := overlay[modelName][resolution]
			if override == nil {
				delete(merged[normalizedModelName], normalizedResolution)
				continue
			}
			base, hasBase := merged[normalizedModelName][normalizedResolution]
			prices, problem := resolveThirdPartySD2Prices(normalizedModelName, normalizedResolution, override, base, hasBase)
			if problem != "" {
				problems = append(problems, problem)
				delete(merged[normalizedModelName], normalizedResolution)
				continue
			}
			merged[normalizedModelName][normalizedResolution] = prices
		}
	}
	return merged, problems
}

func resolveThirdPartySD2Prices(modelName, resolution string, override *ThirdPartySD2ResolutionPricing, base thirdPartySD2Prices, hasBase bool) (thirdPartySD2Prices, string) {
	var prices thirdPartySD2Prices
	fields := []struct {
		name     string
		override *float64
		base     float64
		target   *float64
	}{
		{name: "no_video", override: override.NoVideo, base: base.NoVideo, target: &prices.NoVideo},
		{name: "with_video", override: override.WithVideo, base: base.WithVideo, target: &prices.WithVideo},
	}
	for _, field := range fields {
		switch {
		case field.override != nil:
			value := *field.override
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return prices, fmt.Sprintf("the %s price of %s at %s must be a finite price of 0 or more", field.name, modelName, resolution)
			}
			*field.target = value
		case hasBase:
			*field.target = field.base
		default:
			return prices, fmt.Sprintf("%s at %s has no %s price: set both no_video and with_video for a resolution that has no built-in price", modelName, resolution, field.name)
		}
	}
	return prices, ""
}

// smokeTestThirdPartySD2BillingExprs compiles the expression every model's
// prices render into and prices each configured tier with it. The expression is
// what actually bills the request, so a matrix that renders an unusable one is
// rejected on save instead of failing every request with a 400.
func smokeTestThirdPartySD2BillingExprs(matrix thirdPartySD2ResolvedMatrix) error {
	for _, modelName := range slices.Sorted(maps.Keys(matrix)) {
		resolutions := matrix[modelName]
		expression := buildThirdPartySD2BillingExpr(resolutions)
		if expression == "" {
			// Every resolution of the model was removed; requests for it are
			// rejected before billing.
			continue
		}
		if _, err := billingexpr.CompileFromCache(expression); err != nil {
			return fmt.Errorf("the prices of %s cannot be used for billing: %w", modelName, err)
		}
		if billingexpr.UsesFixedPricing(expression) {
			return fmt.Errorf("the prices of %s render a per-request price, which task billing does not support", modelName)
		}
		for key := range billingexpr.UsedUsageKeys(expression) {
			if !slices.Contains(thirdPartySD2UsageFactKeys, key) {
				return fmt.Errorf("the prices of %s reference the unknown usage fact %q", modelName, key)
			}
		}
		for _, resolution := range thirdPartySD2RankedResolutions(resolutions) {
			for _, variant := range thirdPartySD2Variants(resolution, resolutions[resolution]) {
				if err := smokeTestThirdPartySD2Tier(modelName, expression, resolution, variant); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func smokeTestThirdPartySD2Tier(modelName, expression, resolution string, variant thirdPartySD2Variant) error {
	for _, tokens := range []float64{0, 1, ThirdPartySD2PreConsumedTokenEstimate} {
		cost, trace, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{
			Usage: map[string]any{
				"tokens":            tokens,
				"output_resolution": resolution,
				"video_input":       variant.videoInput,
			},
		})
		if err != nil {
			return fmt.Errorf("pricing %s at %s cannot be evaluated: %w", modelName, variant.tierName, err)
		}
		expected := tokens * variant.price / 1_000_000
		if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			return fmt.Errorf("pricing %s at %s produces an unusable cost for %.0f tokens", modelName, variant.tierName, tokens)
		}
		if math.Abs(cost-expected) > 1e-9*max(1, math.Abs(expected)) {
			return fmt.Errorf("pricing %s at %s charges %g instead of the configured %g for %.0f tokens", modelName, variant.tierName, cost, expected, tokens)
		}
		if trace.MatchedTier != variant.tierName {
			return fmt.Errorf("pricing %s at %s matches the %s price instead", modelName, variant.tierName, trace.MatchedTier)
		}
	}
	return nil
}

func cloneThirdPartySD2PricingMatrix(src ThirdPartySD2PricingMatrix) ThirdPartySD2PricingMatrix {
	if src == nil {
		return nil
	}
	dst := make(ThirdPartySD2PricingMatrix, len(src))
	for modelName, resolutions := range src {
		resolutionMap := make(map[string]*ThirdPartySD2ResolutionPricing, len(resolutions))
		for resolution, pricing := range resolutions {
			if pricing == nil {
				resolutionMap[resolution] = nil
				continue
			}
			copied := *pricing
			if pricing.NoVideo != nil {
				noVideo := *pricing.NoVideo
				copied.NoVideo = &noVideo
			}
			if pricing.WithVideo != nil {
				withVideo := *pricing.WithVideo
				copied.WithVideo = &withVideo
			}
			resolutionMap[resolution] = &copied
		}
		dst[modelName] = resolutionMap
	}
	return dst
}

func cloneThirdPartySD2ResolvedMatrix(src thirdPartySD2ResolvedMatrix) thirdPartySD2ResolvedMatrix {
	dst := make(thirdPartySD2ResolvedMatrix, len(src))
	for modelName, resolutions := range src {
		dst[modelName] = maps.Clone(resolutions)
	}
	return dst
}

// publicThirdPartySD2PricingMatrix renders a resolved matrix as the configured
// shape, with both prices explicit.
func publicThirdPartySD2PricingMatrix(src thirdPartySD2ResolvedMatrix) ThirdPartySD2PricingMatrix {
	dst := make(ThirdPartySD2PricingMatrix, len(src))
	for modelName, resolutions := range src {
		resolutionMap := make(map[string]*ThirdPartySD2ResolutionPricing, len(resolutions))
		for resolution, pricing := range resolutions {
			noVideo, withVideo := pricing.NoVideo, pricing.WithVideo
			resolutionMap[resolution] = &ThirdPartySD2ResolutionPricing{NoVideo: &noVideo, WithVideo: &withVideo}
		}
		dst[modelName] = resolutionMap
	}
	return dst
}

func classifyThirdPartySD2PricingResolution(height int) string {
	switch {
	case height >= 2160:
		return "4k"
	case height >= 1080:
		return "1080p"
	case height >= 720:
		return "720p"
	case height > 0:
		return "480p"
	default:
		return ""
	}
}

func thirdPartySD2ResolutionRank(resolution string) int {
	switch resolution {
	case "480p":
		return 1
	case "720p":
		return 2
	case "1080p":
		return 3
	case "4k":
		return 4
	default:
		return -1
	}
}
