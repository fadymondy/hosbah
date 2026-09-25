package rum

import (
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// Window is one of the three summary windows the team API and MCP tools offer.
type Window string

const (
	Window24h Window = "24h"
	Window7d  Window = "7d"
	Window30d Window = "30d"
)

// Since is the start of a window, and Bucket is the date_trunc field its timeseries groups by
// ('hour' for the 24h window so a day still has enough points to plot; 'day' otherwise).
func (w Window) Since(now time.Time) time.Time {
	switch w {
	case Window7d:
		return now.Add(-7 * 24 * time.Hour)
	case Window30d:
		return now.Add(-30 * 24 * time.Hour)
	default:
		return now.Add(-24 * time.Hour)
	}
}

func (w Window) Bucket() string {
	if w == Window24h {
		return "hour"
	}
	return "day"
}

// ParseWindow defaults an unknown or empty value to 24h.
func ParseWindow(s string) Window {
	switch Window(s) {
	case Window7d:
		return Window7d
	case Window30d:
		return Window30d
	default:
		return Window24h
	}
}

// RumMetrics is the five Core Web Vitals of one summary row (an app overall, a path, or one
// timeseries bucket), each with its p75, sample count and rating.
type RumMetrics struct {
	LCP  Metric `json:"lcp"`
	CLS  Metric `json:"cls"`
	INP  Metric `json:"inp"`
	FCP  Metric `json:"fcp"`
	TTFB Metric `json:"ttfb"`
}

func metricsFrom(lcpP75 float64, lcpN int64, clsP75 float64, clsN int64, inpP75 float64, inpN int64,
	fcpP75 float64, fcpN int64, ttfbP75 float64, ttfbN int64) RumMetrics {
	return RumMetrics{
		LCP:  metric(lcpP75, lcpN, RateLCP),
		CLS:  metric(clsP75, clsN, RateCLS),
		INP:  metric(inpP75, inpN, RateINP),
		FCP:  metric(fcpP75, fcpN, RateFCP),
		TTFB: metric(ttfbP75, ttfbN, RateTTFB),
	}
}

// Summary is an app's Core Web Vitals over a window.
type Summary struct {
	Window     Window     `json:"window"`
	Samples    int64      `json:"samples"`
	RumMetrics RumMetrics `json:"metrics"`
}

func SummaryFrom(w Window, row db.RumSummaryRow) Summary {
	return Summary{Window: w, Samples: row.Samples, RumMetrics: metricsFrom(
		row.LcpP75, row.LcpN, row.ClsP75, row.ClsN, row.InpP75, row.InpN, row.FcpP75, row.FcpN, row.TtfbP75, row.TtfbN)}
}

// PathBreakdown is one path's Core Web Vitals (RumTopPaths: up to the 20 busiest paths).
type PathBreakdown struct {
	Path       string     `json:"path"`
	Samples    int64      `json:"samples"`
	RumMetrics RumMetrics `json:"metrics"`
}

func PathsFrom(rows []db.RumTopPathsRow) []PathBreakdown {
	out := make([]PathBreakdown, 0, len(rows))
	for _, r := range rows {
		out = append(out, PathBreakdown{Path: r.Path, Samples: r.Samples, RumMetrics: metricsFrom(
			r.LcpP75, r.LcpN, r.ClsP75, r.ClsN, r.InpP75, r.InpN, r.FcpP75, r.FcpN, r.TtfbP75, r.TtfbN)})
	}
	return out
}

// RumPoint is one bucket of a p75 timeseries.
type RumPoint struct {
	Bucket     time.Time  `json:"bucket"`
	Samples    int64      `json:"samples"`
	RumMetrics RumMetrics `json:"metrics"`
}

func TimeseriesFrom(rows []db.RumTimeseriesRow) []RumPoint {
	out := make([]RumPoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, RumPoint{Bucket: r.Bucket, Samples: r.Samples, RumMetrics: metricsFrom(
			r.LcpP75, r.LcpN, r.ClsP75, r.ClsN, r.InpP75, r.InpN, r.FcpP75, r.FcpN, r.TtfbP75, r.TtfbN)})
	}
	return out
}

// NewKey mints a fresh ingest key and its hash (the only thing stored, in app_rum_settings.key_hash).
func NewKey() (plain, hash string) { return tokens.New(tokens.PrefixRum) }

// Snippet is the copy-paste <script> tag shown once, right after enabling RUM or rotating its key.
func Snippet(publicURL, key string) string {
	return `<script defer src="` + publicURL + `/rum.js" data-key="` + key + `"></script>`
}
