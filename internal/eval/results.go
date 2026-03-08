package eval

import (
	"math"

	"github.com/tripledoublev/v100/internal/core"
)

// VariantStats holds aggregated research metrics for a variant.
type VariantStats struct {
	VariantName string
	Trials      int
	PassRate    float64
	CI95Low     float64 // 95% Confidence Interval (Wilson Score)
	CI95High    float64
	MeanTokens  float64
	MeanCost    float64
	MeanSteps   float64
	MeanTTFT    float64
}

// AggregateResults computes statistics across multiple trial runs.
func AggregateResults(variantName string, metrics []core.RunMetrics) VariantStats {
	if len(metrics) == 0 {
		return VariantStats{VariantName: variantName}
	}

	var (
		totalTokens float64
		totalCost   float64
		totalSteps  float64
		passes      int
	)

	for _, m := range metrics {
		totalTokens += float64(m.TokensPerRun)
		totalCost += m.CostPerRunUSD
		totalSteps += float64(m.StepsPerRun)
		// Assuming m.EfficiencyScore > 80 is a "Pass" for this research layer
		// (In practice, this would come from a pluggable Scorer result)
		if m.EfficiencyScore >= 80 {
			passes++
		}
	}

	n := float64(len(metrics))
	passRate := float64(passes) / n
	low, high := WilsonScoreInterval(passRate, len(metrics), 1.96) // 1.96 = 95% confidence

	return VariantStats{
		VariantName: variantName,
		Trials:      len(metrics),
		PassRate:    passRate,
		CI95Low:     low,
		CI95High:    high,
		MeanTokens:  totalTokens / n,
		MeanCost:    totalCost / n,
		MeanSteps:   totalSteps / n,
	}
}

// WilsonScoreInterval calculates the confidence interval for a proportion.
// Standard research practice for small-N agent evaluations.
func WilsonScoreInterval(p float64, n int, z float64) (float64, float64) {
	if n == 0 {
		return 0, 0
	}
	nf := float64(n)
	denominator := 1 + z*z/nf
	centreAdjustedProbability := p + z*z/(2*nf)
	adjustedStandardDeviation := math.Sqrt((p*(1-p) + z*z/(4*nf)) / nf)

	lower := (centreAdjustedProbability - z*adjustedStandardDeviation) / denominator
	upper := (centreAdjustedProbability + z*adjustedStandardDeviation) / denominator

	return math.Max(0, lower), math.Min(1, upper)
}
