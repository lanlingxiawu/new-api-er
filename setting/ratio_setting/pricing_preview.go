package ratio_setting

import "strings"

func GetDefaultPricingValue(key, name string) (float64, bool) {
	var entries map[string]float64
	switch key {
	case "ModelRatio":
		entries = defaultModelRatio
	case "ModelPrice":
		entries = defaultModelPrice
	case "CompletionRatio":
		entries = defaultCompletionRatio
	case "CacheRatio":
		entries = defaultCacheRatio
	case "CreateCacheRatio":
		entries = defaultCreateCacheRatio
	case "ImageRatio":
		entries = defaultImageRatio
	case "AudioRatio":
		entries = defaultAudioRatio
	case "AudioCompletionRatio":
		entries = defaultAudioCompletionRatio
	}
	value, ok := entries[name]
	return value, ok
}

// ResolvePricingCompletionRatio applies the existing billing rules to a draft,
// without reading or changing the currently published administrator override.
func ResolvePricingCompletionRatio(name string, configured *float64) CompletionRatioInfo {
	name = FormatMatchingModelName(name)
	if strings.Contains(name, "/") && configured != nil {
		return CompletionRatioInfo{Ratio: *configured}
	}
	fallback, locked := getHardcodedCompletionModelRatio(name)
	if locked {
		return CompletionRatioInfo{Ratio: fallback, Locked: true}
	}
	if configured != nil {
		return CompletionRatioInfo{Ratio: *configured}
	}
	return CompletionRatioInfo{Ratio: fallback}
}
