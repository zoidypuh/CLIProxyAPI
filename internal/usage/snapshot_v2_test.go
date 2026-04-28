package usage

import (
	"math"
	"testing"
)

func TestWeightedPriceScorePricesCachedInputAtCacheReadRate(t *testing.T) {
	got := WeightedPriceScore(189697, 1066, 187776)
	const want = 135473
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("WeightedPriceScore() = %v, want %v", got, want)
	}
}
