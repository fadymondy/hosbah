// Package rum is real-user monitoring for apps deployed on Hosbah (MH-575 H5): a per-app toggle
// and a rotatable, non-secret ingest key; a tiny first-party beacon (GET /rum.js) that collects
// Core Web Vitals in the visitor's own browser; a public ingest endpoint (POST /api/rum/{key});
// and the team-scoped summaries served under /api/teams/{team}/apps/{app}/rum (internal/api).
package rum

// Rating is a Core Web Vitals verdict: good, needs-improvement or poor.
type Rating string

const (
	RatingGood      Rating = "good"
	RatingNeedsWork Rating = "needs-improvement"
	RatingPoor      Rating = "poor"
	// RatingUnknown: no samples reported this metric in the window (never confused with "0 and good").
	RatingUnknown Rating = "unknown"
)

// thresholds is [good ceiling, poor floor) per Google's published Core Web Vitals thresholds
// (web.dev/articles/lcp, /cls, /inp; FCP and TTFB are the same site's "other metrics" guidance).
// A p75 at or under Good is good; at or over Poor is poor; between is needs-improvement.
type thresholds struct{ good, poor float64 }

var (
	lcpThresholds  = thresholds{good: 2500, poor: 4000} // ms
	clsThresholds  = thresholds{good: 0.1, poor: 0.25}  // unitless
	inpThresholds  = thresholds{good: 200, poor: 500}   // ms
	fcpThresholds  = thresholds{good: 1800, poor: 3000} // ms
	ttfbThresholds = thresholds{good: 800, poor: 1800}  // ms
)

func rate(v float64, t thresholds) Rating {
	switch {
	case v <= t.good:
		return RatingGood
	case v >= t.poor:
		return RatingPoor
	default:
		return RatingNeedsWork
	}
}

// RateLCP, RateCLS, RateINP, RateFCP, RateTTFB rate a p75 value per Google's published thresholds.
func RateLCP(p75 float64) Rating  { return rate(p75, lcpThresholds) }
func RateCLS(p75 float64) Rating  { return rate(p75, clsThresholds) }
func RateINP(p75 float64) Rating  { return rate(p75, inpThresholds) }
func RateFCP(p75 float64) Rating  { return rate(p75, fcpThresholds) }
func RateTTFB(p75 float64) Rating { return rate(p75, ttfbThresholds) }

// Metric is one Core Web Vitals metric's p75 over a window, with its rating and how many samples
// it is based on (0 => Rating is RatingUnknown regardless of the p75 value, which is meaningless
// with no data — see the *_n columns in internal/db/queries/rum_sample.sql).
type Metric struct {
	P75     float64 `json:"p75"`
	Samples int64   `json:"samples"`
	Rating  Rating  `json:"rating"`
}

func metric(p75 float64, n int64, rateFn func(float64) Rating) Metric {
	if n == 0 {
		return Metric{P75: 0, Samples: 0, Rating: RatingUnknown}
	}
	return Metric{P75: round2(p75), Samples: n, Rating: rateFn(p75)}
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
