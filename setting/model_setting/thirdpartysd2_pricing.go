package model_setting

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type ThirdPartySD2ResolutionPricing struct {
	NoVideo   float64 `json:"no_video"`
	WithVideo float64 `json:"with_video"`
}

type ThirdPartySD2PricingMatrix map[string]map[string]ThirdPartySD2ResolutionPricing

type ThirdPartySD2PricingSettings struct {
	Matrix ThirdPartySD2PricingMatrix `json:"matrix"`
}

var defaultThirdPartySD2PricingMatrix = ThirdPartySD2PricingMatrix{
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
	Matrix: cloneThirdPartySD2PricingMatrix(defaultThirdPartySD2PricingMatrix),
}

type thirdPartySD2PricingIndex struct {
	matrix ThirdPartySD2PricingMatrix
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
	merged := cloneThirdPartySD2PricingMatrix(defaultThirdPartySD2PricingMatrix)
	overlayThirdPartySD2PricingMatrix(merged, thirdPartySD2PricingSettings.Matrix)
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
// expression returns the task cost in USD.
func GetThirdPartySD2BillingExpr(modelName string) (string, bool) {
	idx := currentThirdPartySD2PricingIndex.Load()
	if idx == nil {
		return "", false
	}
	expression, ok := idx.billingExprs[strings.TrimSpace(modelName)]
	return expression, ok
}

func buildThirdPartySD2BillingExprs(matrix ThirdPartySD2PricingMatrix) map[string]string {
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
// plugin rejects resolutions a model does not support before billing and the
// merged matrix always keeps the default combinations, so the final else is
// the last listed tier instead of an unpriced fallback.
func buildThirdPartySD2BillingExpr(resolutions map[string]ThirdPartySD2ResolutionPricing) string {
	keys := make([]string, 0, len(resolutions))
	for resolution := range resolutions {
		if thirdPartySD2ResolutionRank(resolution) > 0 {
			keys = append(keys, resolution)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Slice(keys, func(i, j int) bool {
		return thirdPartySD2ResolutionRank(keys[i]) < thirdPartySD2ResolutionRank(keys[j])
	})
	type branch struct{ condition, tier string }
	branches := make([]branch, 0, len(keys)*2)
	for _, resolution := range keys {
		pricing := resolutions[resolution]
		variants := []struct {
			videoInput string
			tierName   string
			price      float64
		}{
			{"none", resolution, pricing.NoVideo},
			{"video", resolution + "_video", pricing.WithVideo},
		}
		for _, variant := range variants {
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

func GetThirdPartySD2PricingMatrix() ThirdPartySD2PricingMatrix {
	idx := currentThirdPartySD2PricingIndex.Load()
	if idx == nil || idx.matrix == nil {
		return cloneThirdPartySD2PricingMatrix(defaultThirdPartySD2PricingMatrix)
	}
	return cloneThirdPartySD2PricingMatrix(idx.matrix)
}

// GetThirdPartySD2Resolutions returns the resolutions priced for a model in the
// merged matrix, ordered by rank. A model accepts exactly these resolutions.
func GetThirdPartySD2Resolutions(modelName string) ([]string, bool) {
	idx := currentThirdPartySD2PricingIndex.Load()
	if idx == nil || idx.matrix == nil {
		return nil, false
	}
	resolutions, ok := idx.matrix[strings.TrimSpace(modelName)]
	if !ok {
		return nil, false
	}
	keys := make([]string, 0, len(resolutions))
	for resolution := range resolutions {
		if thirdPartySD2ResolutionRank(resolution) > 0 {
			keys = append(keys, resolution)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return thirdPartySD2ResolutionRank(keys[i]) < thirdPartySD2ResolutionRank(keys[j])
	})
	return keys, true
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

	for modelName, resolutions := range matrix {
		normalizedModelName := strings.TrimSpace(modelName)
		if normalizedModelName == "" {
			return fmt.Errorf("model name cannot be empty")
		}
		if resolutions == nil {
			return fmt.Errorf("model %s must map to an object", modelName)
		}
		normalizedResolutions := make(map[string]struct{}, len(resolutions))
		for resolution, pricing := range resolutions {
			normalizedResolution := NormalizeThirdPartySD2Resolution(resolution)
			if normalizedResolution == "" {
				return fmt.Errorf("resolution cannot be empty for model %s", modelName)
			}
			if _, exists := normalizedResolutions[normalizedResolution]; exists {
				return fmt.Errorf("duplicate resolution %s after normalization for model %s", resolution, normalizedModelName)
			}
			normalizedResolutions[normalizedResolution] = struct{}{}
			if pricing.NoVideo < 0 {
				return fmt.Errorf("price cannot be negative for %s %s no_video", modelName, resolution)
			}
			if pricing.WithVideo < 0 {
				return fmt.Errorf("price cannot be negative for %s %s with_video", modelName, resolution)
			}
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
		resolutionMap := make(map[string]ThirdPartySD2ResolutionPricing, len(resolutions))
		for resolution, pricing := range resolutions {
			resolutionMap[resolution] = pricing
		}
		dst[modelName] = resolutionMap
	}
	return dst
}

func overlayThirdPartySD2PricingMatrix(dst, src ThirdPartySD2PricingMatrix) {
	for modelName, resolutions := range src {
		normalizedModelName := strings.TrimSpace(modelName)
		if normalizedModelName == "" {
			continue
		}
		if dst[normalizedModelName] == nil {
			dst[normalizedModelName] = make(map[string]ThirdPartySD2ResolutionPricing, len(resolutions))
		}
		for resolution, pricing := range resolutions {
			normalizedResolution := NormalizeThirdPartySD2Resolution(resolution)
			if normalizedResolution == "" {
				continue
			}
			dst[normalizedModelName][normalizedResolution] = pricing
		}
	}
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
