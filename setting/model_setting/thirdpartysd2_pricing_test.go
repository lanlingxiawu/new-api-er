package model_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetThirdPartySD2TokenPriceUsesMergedMatrix(t *testing.T) {
	original := cloneThirdPartySD2PricingMatrix(thirdPartySD2PricingSettings.Matrix)
	t.Cleanup(func() {
		thirdPartySD2PricingSettings.Matrix = original
		RebuildThirdPartySD2PricingIndex()
	})

	thirdPartySD2PricingSettings.Matrix = ThirdPartySD2PricingMatrix{
		"dreamina-seedance-2-0-260128": {
			"1080p": {
				NoVideo:   9.9,
				WithVideo: 6.6,
			},
		},
	}
	RebuildThirdPartySD2PricingIndex()

	price, ok := GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-260128", "1080p", false)
	require.True(t, ok)
	require.Equal(t, 9.9, price)

	fallbackPrice, ok := GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-260128", "720p", true)
	require.True(t, ok)
	require.Equal(t, 4.3, fallbackPrice)
}

func TestValidateThirdPartySD2PricingMatrixJSONRejectsNegativeValue(t *testing.T) {
	err := ValidateThirdPartySD2PricingMatrixJSON(`{
		"dreamina-seedance-2-0-260128": {
			"720p": {
				"no_video": -1,
				"with_video": 4.3
			}
		}
	}`)
	require.Error(t, err)
}

func TestGetThirdPartySD2TokenPriceNormalizesResolutionKeys(t *testing.T) {
	original := cloneThirdPartySD2PricingMatrix(thirdPartySD2PricingSettings.Matrix)
	t.Cleanup(func() {
		thirdPartySD2PricingSettings.Matrix = original
		RebuildThirdPartySD2PricingIndex()
	})

	thirdPartySD2PricingSettings.Matrix = ThirdPartySD2PricingMatrix{
		"dreamina-seedance-2-0-fast-260128": {
			"720P": {
				NoVideo:   6.1,
				WithVideo: 3.6,
			},
			"2160P": {
				NoVideo:   8.8,
				WithVideo: 5.5,
			},
		},
	}
	RebuildThirdPartySD2PricingIndex()

	price, ok := GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-fast-260128", "720p", false)
	require.True(t, ok)
	require.Equal(t, 6.1, price)

	price, ok = GetThirdPartySD2TokenPrice("dreamina-seedance-2-0-fast-260128", "4k", true)
	require.True(t, ok)
	require.Equal(t, 5.5, price)
}

func TestMaxThirdPartySD2ResolutionPrefersLargestTier(t *testing.T) {
	require.Equal(t, "1080p", MaxThirdPartySD2Resolution("480p", "1920x1080"))
	require.Equal(t, "4k", MaxThirdPartySD2Resolution("2160p", "1080p"))
	require.Equal(t, "720p", MaxThirdPartySD2Resolution("", "1280x720"))
}

func TestThirdPartySD2PriceToModelRatioUsesQuotaPerUnit(t *testing.T) {
	require.InDelta(t, 2.35, ThirdPartySD2PriceToModelRatio(4.7), 1e-9)
}

func TestCalculateThirdPartySD2QuotaUsesTokenCount(t *testing.T) {
	require.Equal(t, int(4.7*common.QuotaPerUnit*1.5), CalculateThirdPartySD2Quota(4.7, 1_000_000, 1.5))
	require.Equal(t, int(2.35*common.QuotaPerUnit*1.5), CalculateThirdPartySD2Quota(4.7, 500_000, 1.5))
}
