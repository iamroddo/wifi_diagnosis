package diag

import (
	"testing"
)

func TestLatencyStats_Basic(t *testing.T) {
	samples := []float64{10, 12, 11, 13, 9, 14, 10, 11}
	r := LatencyStats(samples, len(samples))

	if r.Min != 9 {
		t.Errorf("Min: got %v, want 9", r.Min)
	}
	if r.Max != 14 {
		t.Errorf("Max: got %v, want 14", r.Max)
	}
	if r.Count != 8 {
		t.Errorf("Count: got %v, want 8", r.Count)
	}
	if r.Loss != 0 {
		t.Errorf("Loss: got %v, want 0", r.Loss)
	}
	if r.Avg <= 0 {
		t.Errorf("Avg: got %v, want > 0", r.Avg)
	}
	if r.P95 < r.Median {
		t.Errorf("P95 (%v) < Median (%v)", r.P95, r.Median)
	}
	if r.Jitter < 0 {
		t.Errorf("Jitter: got %v, want >= 0", r.Jitter)
	}
}

func TestLatencyStats_WithLoss(t *testing.T) {
	samples := []float64{10, 12, 11}
	r := LatencyStats(samples, 10) // 10 sent, 3 received → 70% loss
	if r.Loss != 70 {
		t.Errorf("Loss: got %v, want 70", r.Loss)
	}
}

func TestLatencyStats_Empty(t *testing.T) {
	r := LatencyStats(nil, 5)
	if r.Loss != 100 {
		t.Errorf("Loss: got %v, want 100", r.Loss)
	}
	if r.Count != 0 {
		t.Errorf("Count: got %v, want 0", r.Count)
	}
}

func TestLatencyStats_Single(t *testing.T) {
	r := LatencyStats([]float64{42.5}, 1)
	if r.Min != 42.5 {
		t.Errorf("Min: got %v", r.Min)
	}
	if r.Max != 42.5 {
		t.Errorf("Max: got %v", r.Max)
	}
	if r.Loss != 0 {
		t.Errorf("Loss: got %v", r.Loss)
	}
}

func TestPercentile(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p50 := percentile(sorted, 50)
	if p50 < 5 || p50 > 6 {
		t.Errorf("p50: got %v, want ~5.5", p50)
	}
	p95 := percentile(sorted, 95)
	if p95 < 9 || p95 > 10 {
		t.Errorf("p95: got %v, want ~9.5", p95)
	}
}
