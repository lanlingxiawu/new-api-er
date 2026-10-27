package billingexpr

import (
	"math"
	"sort"
)

type StandardDifference struct {
	Min      int64   `json:"min"`
	Max      *int64  `json:"max,omitempty"`
	Variable string  `json:"variable"`
	Left     float64 `json:"left"`
	Right    float64 `json:"right"`
}

// CompareStandard compares the complete integer context domain after taking the
// union of both sets of tier boundaries. Inputs must come from AnalyzeStandard.
func CompareStandard(left, right *StandardAnalysis) []StandardDifference {
	boundaries := map[int64]bool{0: true}
	for _, a := range []*StandardAnalysis{left, right} {
		for _, s := range a.Segments {
			boundaries[s.Min] = true
		}
	}
	ordered := make([]int64, 0, len(boundaries))
	for b := range boundaries {
		ordered = append(ordered, b)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	var differences []StandardDifference
	li, ri := 0, 0
	for i, start := range ordered {
		for li+1 < len(left.Segments) && left.Segments[li+1].Min <= start {
			li++
		}
		for ri+1 < len(right.Segments) && right.Segments[ri+1].Min <= start {
			ri++
		}
		l, r := left.EffectivePrices(left.Segments[li]), right.EffectivePrices(right.Segments[ri])
		var end *int64
		if i+1 < len(ordered) {
			n := ordered[i+1] - 1
			end = &n
		}
		for _, key := range []string{"p", "c", "cr", "cc", "cc1h", "cc12h", "img_cr", "img", "img_o", "ai", "ao"} {
			if math.Abs(l[key]-r[key]) > 1e-9*math.Max(1, math.Max(math.Abs(l[key]), math.Abs(r[key]))) {
				differences = append(differences, StandardDifference{Min: start, Max: end, Variable: key, Left: l[key], Right: r[key]})
			}
		}
	}
	return differences
}
