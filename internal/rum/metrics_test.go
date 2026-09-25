package rum

import "testing"

func TestRateLCP(t *testing.T) {
	cases := map[float64]Rating{
		0:    RatingGood,
		2500: RatingGood,
		2501: RatingNeedsWork,
		3999: RatingNeedsWork,
		4000: RatingPoor,
		9000: RatingPoor,
	}
	for v, want := range cases {
		if got := RateLCP(v); got != want {
			t.Errorf("RateLCP(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestRateCLS(t *testing.T) {
	cases := map[float64]Rating{
		0:    RatingGood,
		0.1:  RatingGood,
		0.11: RatingNeedsWork,
		0.24: RatingNeedsWork,
		0.25: RatingPoor,
		1:    RatingPoor,
	}
	for v, want := range cases {
		if got := RateCLS(v); got != want {
			t.Errorf("RateCLS(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestRateINP(t *testing.T) {
	cases := map[float64]Rating{
		0:   RatingGood,
		200: RatingGood,
		201: RatingNeedsWork,
		499: RatingNeedsWork,
		500: RatingPoor,
		900: RatingPoor,
	}
	for v, want := range cases {
		if got := RateINP(v); got != want {
			t.Errorf("RateINP(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestRateFCP(t *testing.T) {
	cases := map[float64]Rating{
		0:    RatingGood,
		1800: RatingGood,
		1801: RatingNeedsWork,
		2999: RatingNeedsWork,
		3000: RatingPoor,
		5000: RatingPoor,
	}
	for v, want := range cases {
		if got := RateFCP(v); got != want {
			t.Errorf("RateFCP(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestRateTTFB(t *testing.T) {
	cases := map[float64]Rating{
		0:    RatingGood,
		800:  RatingGood,
		801:  RatingNeedsWork,
		1799: RatingNeedsWork,
		1800: RatingPoor,
		3000: RatingPoor,
	}
	for v, want := range cases {
		if got := RateTTFB(v); got != want {
			t.Errorf("RateTTFB(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestMetricZeroSamplesIsUnknown(t *testing.T) {
	m := metric(1234, 0, RateLCP)
	if m.Rating != RatingUnknown {
		t.Errorf("expected RatingUnknown with 0 samples, got %v", m.Rating)
	}
	if m.P75 != 0 || m.Samples != 0 {
		t.Errorf("expected zeroed P75/Samples with 0 samples, got %+v", m)
	}
}

func TestMetricWithSamplesRatesAndRounds(t *testing.T) {
	m := metric(2499.126, 42, RateLCP)
	if m.Rating != RatingGood {
		t.Errorf("expected good rating, got %v", m.Rating)
	}
	if m.Samples != 42 {
		t.Errorf("expected 42 samples, got %d", m.Samples)
	}
	if m.P75 != 2499.13 {
		t.Errorf("expected rounded p75 2499.13, got %v", m.P75)
	}
}

func TestRound2(t *testing.T) {
	cases := map[float64]float64{
		1.005:   1.0, // banker's-adjacent float noise; document actual behaviour rather than assume
		1.004:   1.0,
		1.006:   1.01,
		0:       0,
		2500.0:  2500.0,
		0.09999: 0.1,
	}
	for in, want := range cases {
		if got := round2(in); got != want {
			t.Errorf("round2(%v) = %v, want %v", in, got, want)
		}
	}
}
