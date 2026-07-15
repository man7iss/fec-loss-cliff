package stats

import (
	"math"
	"testing"
)

func approx(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestMedian(t *testing.T) {
	t.Run("odd count", func(t *testing.T) {
		if got := Median([]float64{3, 1, 2}); got != 2 {
			t.Fatalf("median=%v, want 2", got)
		}
	})
	t.Run("even count averages middle two", func(t *testing.T) {
		if got := Median([]float64{4, 1, 3, 2}); got != 2.5 {
			t.Fatalf("median=%v, want 2.5", got)
		}
	})
	t.Run("does not mutate input", func(t *testing.T) {
		in := []float64{3, 1, 2}
		Median(in)
		if in[0] != 3 || in[1] != 1 || in[2] != 2 {
			t.Fatalf("input mutated: %v", in)
		}
	})
	t.Run("empty is zero", func(t *testing.T) {
		if got := Median(nil); got != 0 {
			t.Fatalf("median=%v, want 0", got)
		}
	})
}

func TestMean(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		if got := Mean([]float64{1, 2, 3, 4}); got != 2.5 {
			t.Fatalf("mean=%v, want 2.5", got)
		}
	})
	t.Run("empty is zero", func(t *testing.T) {
		if got := Mean(nil); got != 0 {
			t.Fatalf("mean=%v, want 0", got)
		}
	})
}

func TestCV(t *testing.T) {
	t.Run("zero variance is zero", func(t *testing.T) {
		if got := CV([]float64{5, 5, 5}); got != 0 {
			t.Fatalf("cv=%v, want 0", got)
		}
	})
	t.Run("known value", func(t *testing.T) {
		// population stddev of {2,4,4,4,5,5,7,9} is 2, mean is 5, cv = 40%.
		got := CV([]float64{2, 4, 4, 4, 5, 5, 7, 9})
		if !approx(got, 40) {
			t.Fatalf("cv=%v, want 40", got)
		}
	})
	t.Run("single sample is zero", func(t *testing.T) {
		if got := CV([]float64{7}); got != 0 {
			t.Fatalf("cv=%v, want 0", got)
		}
	})
}

func TestJain(t *testing.T) {
	t.Run("perfect fairness is one", func(t *testing.T) {
		if got := Jain([]float64{10, 10, 10}); !approx(got, 1) {
			t.Fatalf("jain=%v, want 1", got)
		}
	})
	t.Run("one flow starved approaches one over n", func(t *testing.T) {
		// two flows, one gets everything: index = 0.5.
		if got := Jain([]float64{10, 0}); !approx(got, 0.5) {
			t.Fatalf("jain=%v, want 0.5", got)
		}
	})
	t.Run("empty is zero", func(t *testing.T) {
		if got := Jain(nil); got != 0 {
			t.Fatalf("jain=%v, want 0", got)
		}
	})
}
