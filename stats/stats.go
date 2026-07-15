// Package stats provides the summary statistics the harness reports per matrix
// cell: median and coefficient of variation over repetitions, and Jain's
// fairness index for the two-flow bottleneck experiment.
package stats

import (
	"math"
	"sort"
)

// Median returns the median of xs without mutating it. Empty input returns 0.
func Median(xs []float64) float64 {
	var sorted []float64

	if len(xs) == 0 {
		return 0
	}
	sorted = append(sorted, xs...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// Mean returns the arithmetic mean of xs. Empty input returns 0.
func Mean(xs []float64) float64 {
	var sum float64

	if len(xs) == 0 {
		return 0
	}
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// CV returns the coefficient of variation (population standard deviation over
// mean) as a percentage. It returns 0 when there are fewer than two samples or
// the mean is zero.
func CV(xs []float64) float64 {
	var (
		mean = Mean(xs)
		sum  float64
	)

	if len(xs) < 2 || mean == 0 {
		return 0
	}
	for _, x := range xs {
		d := x - mean
		sum += d * d
	}
	return math.Sqrt(sum/float64(len(xs))) / mean * 100
}

// Jain returns Jain's fairness index for xs: (sum x)^2 / (n * sum x^2). The
// value ranges from 1/n for maximal unfairness to 1 for perfect fairness. Empty
// input returns 0.
func Jain(xs []float64) float64 {
	var (
		sum   float64
		sumSq float64
	)

	if len(xs) == 0 {
		return 0
	}
	for _, x := range xs {
		sum += x
		sumSq += x * x
	}
	if sumSq == 0 {
		return 0
	}
	return sum * sum / (float64(len(xs)) * sumSq)
}
