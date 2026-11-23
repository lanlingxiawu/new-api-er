package auditsecurity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeIPIntelligencePreservesEvidenceSemantics(t *testing.T) {
	merged := IPIntelligenceSnapshot{FieldProviders: map[string]string{}}
	source := IPIntelligenceSnapshot{FieldProviders: map[string]string{"geo": "response"}, Country: "United States", ObservedAt: 100}
	mergeIPIntelligence(&merged, source, "first")
	require.Equal(t, int64(100), merged.ObservedAt)
	mergeIPIntelligence(&merged, IPIntelligenceSnapshot{ObservedAt: 500}, "empty")
	require.Equal(t, int64(100), merged.ObservedAt)

	anycast, satellite := true, true
	mergeIPIntelligence(&merged, IPIntelligenceSnapshot{IsAnycast: &anycast, IsSatellite: &satellite}, "flagged")
	falseValue := false
	mergeIPIntelligence(&merged, IPIntelligenceSnapshot{IsAnycast: &falseValue, IsSatellite: &falseValue}, "clean")
	require.True(t, *merged.IsAnycast)
	require.True(t, *merged.IsSatellite)

	merged.Country = "United States"
	merged.State = ""
	mergeIPIntelligence(&merged, IPIntelligenceSnapshot{FieldProviders: map[string]string{"geo": "response"}, Country: "France", State: "Brittany"}, "other")
	require.Equal(t, "United States", merged.Country)
	require.Empty(t, merged.State, "equal-quality sources must not be stitched into one location")

	lat, lon := 48.0, -1.8
	mergeIPIntelligence(&merged, IPIntelligenceSnapshot{FieldProviders: map[string]string{"geo": "response"}, Country: "France", State: "Brittany", Latitude: &lat, Longitude: &lon, Accuracy: "HIGH"}, "precise")
	require.Equal(t, "France", merged.Country)
	require.Equal(t, "Brittany", merged.State)
	require.Equal(t, "precise", merged.FieldProviders["geo"])
}
