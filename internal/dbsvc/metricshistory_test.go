package dbsvc

import (
	"testing"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func TestBucketMetricsAverages(t *testing.T) {
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	rows := []db.DataServiceMetricsSinceRow{
		{At: base, Connections: 10, MaxConnections: 100, CacheHitRatio: 98.0, SizeBytes: 1000, SlowQueryCount: 0, RatePerSec: 5},
		{At: base.Add(30 * time.Second), Connections: 20, MaxConnections: 100, CacheHitRatio: 99.0, SizeBytes: 1100, SlowQueryCount: 1, RatePerSec: 7},
		// A bucket a whole minute later: its own point, not merged with the first.
		{At: base.Add(time.Minute), Connections: 40, MaxConnections: 110, CacheHitRatio: 97.0, SizeBytes: 1200, SlowQueryCount: 2, RatePerSec: 9},
	}
	points := bucketMetrics(rows, base.Add(-time.Hour), time.Minute)
	if len(points) != 2 {
		t.Fatalf("expected 2 buckets, got %d: %+v", len(points), points)
	}
	if points[0].Connections != 15 { // (10+20)/2
		t.Errorf("bucket 0 connections = %v, want 15", points[0].Connections)
	}
	if points[0].MaxConnections != 100 {
		t.Errorf("bucket 0 max_connections = %v, want 100", points[0].MaxConnections)
	}
	if points[0].CacheHitRatio != 98.5 {
		t.Errorf("bucket 0 cache_hit_ratio = %v, want 98.5", points[0].CacheHitRatio)
	}
	if points[0].SizeBytes != 1100 {
		t.Errorf("bucket 0 size_bytes = %v, want 1100 (max)", points[0].SizeBytes)
	}
	if points[0].RatePerSec != 6 { // (5+7)/2
		t.Errorf("bucket 0 rate_per_sec = %v, want 6", points[0].RatePerSec)
	}
	if points[1].Connections != 40 {
		t.Errorf("bucket 1 connections = %v, want 40", points[1].Connections)
	}
}

func TestBucketMetricsDropsBeforeSince(t *testing.T) {
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	rows := []db.DataServiceMetricsSinceRow{
		{At: base.Add(-time.Hour), Connections: 999}, // before the window: dropped
		{At: base, Connections: 5},
	}
	points := bucketMetrics(rows, base.Add(-time.Minute), time.Minute)
	if len(points) != 1 {
		t.Fatalf("expected 1 bucket (the stale row dropped), got %d: %+v", len(points), points)
	}
	if points[0].Connections != 5 {
		t.Errorf("connections = %v, want 5", points[0].Connections)
	}
}

func TestMetricsWindowAlignsToStep(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 34, 17, 0, time.UTC)
	since := metricsWindow(now, time.Hour, time.Minute)
	if since.Second() != 0 || since.Nanosecond() != 0 {
		t.Errorf("since should align to the minute: %v", since)
	}
	if !since.Before(now) || now.Sub(since) > time.Hour+time.Minute {
		t.Errorf("since %v should be about an hour before now %v", since, now)
	}
}
