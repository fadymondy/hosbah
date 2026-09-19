package metrics

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestRangesStayUnder300Points(t *testing.T) {
	for _, k := range RangeKeys {
		r := Ranges[k]
		if n := int(r.Span / r.Step); n > 300 || n < 30 {
			t.Errorf("%s: %d buckets", k, n)
		}
		if n := int(r.Span / SparkStep(r)); n != MaxSparkPoints {
			t.Errorf("%s sparkline: %d buckets", k, n)
		}
	}
}

func TestWindowHasExactlySpanOverStepBuckets(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 34, 56, 0, time.UTC)
	since := window(now, time.Hour, time.Minute)
	if want := time.Date(2026, 9, 19, 11, 35, 0, 0, time.UTC); !since.Equal(want) {
		t.Fatalf("since %v, want %v", since, want)
	}
	var samples []Sample
	for at := since.Add(-10 * time.Minute); !at.After(now); at = at.Add(15 * time.Second) {
		samples = append(samples, Sample{At: at, Samples: 1, CPU: 10})
	}
	if pts := Bucket(samples, since, time.Minute); len(pts) != 60 {
		t.Fatalf("%d points, want 60", len(pts))
	}
}

func TestBucketWeightsBySamples(t *testing.T) {
	t0 := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	g := func(util float64) string {
		b, _ := json.Marshal([]agentproto.GPUMetric{{Index: 0, UtilPercent: util, MemTotalBytes: 100}})
		return string(b)
	}
	pts := Bucket([]Sample{
		{At: t0, Samples: 3, CPU: 10, MemUsed: 100, MemTotal: 1000, GPU: g(30)},
		{At: t0.Add(time.Minute), Samples: 1, CPU: 50, MemUsed: 300, MemTotal: 1000, GPU: "[]"},
		{At: t0.Add(5 * time.Minute), Samples: 1, CPU: 70, MemTotal: 1000, GPU: g(90)},
	}, t0, 5*time.Minute)
	if len(pts) != 2 {
		t.Fatalf("%d points", len(pts))
	}
	p := pts[0]
	if p.CPU != 20 || p.MemUsed != 200 || p.MemTotal != 1000 || len(p.GPUs) != 1 || p.GPUs[0].Util != 30 || p.GPUs[0].MemTotal != 100 {
		t.Fatalf("first bucket %+v", p)
	}
	if pts[1].CPU != 70 || !pts[1].At.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("second bucket %+v", pts[1])
	}
	if sp := sparkOf(pts); len(sp.CPU) != 2 || sp.Mem[0] != 20 || sp.GPU == nil || sp.GPU[1] != 90 {
		t.Fatalf("spark %+v", sp)
	}
	if sp := sparkOf([]Point{{CPU: 1, GPUs: []GPUPoint{}}}); sp.GPU != nil {
		t.Fatalf("no GPU should be a null series: %+v", sp)
	}
}

func TestMinuteAccumulates(t *testing.T) {
	var mm Minute
	t0 := time.Date(2026, 9, 19, 12, 0, 5, 0, time.UTC)
	gpu := []agentproto.GPUMetric{{Index: 0, UtilPercent: 50}}
	if done := mm.Add(agentproto.Metrics{CPUPercent: 10, MemUsedBytes: 1, GPUs: gpu}, t0); done != nil {
		t.Fatal("nothing should be done yet")
	}
	mm.Add(agentproto.Metrics{CPUPercent: 30, MemUsedBytes: 2}, t0.Add(50*time.Second))
	done := mm.Add(agentproto.Metrics{CPUPercent: 99}, t0.Add(60*time.Second))
	if done == nil || done.N != 2 || done.cpu/float64(done.N) != 20 || done.memUsed != 2 || len(done.gpus) != 1 ||
		!done.At.Equal(time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("finished minute %+v", done)
	}
	if mm.N != 1 || mm.cpu != 99 || !mm.At.Equal(time.Date(2026, 9, 19, 12, 1, 0, 0, time.UTC)) {
		t.Fatalf("next minute %+v", mm)
	}
}

func TestLivePoint(t *testing.T) {
	at := time.Now()
	p := LivePoint(agentproto.Metrics{CPUPercent: 12.5, MemTotalBytes: 8, GPUs: []agentproto.GPUMetric{{Index: 1, UtilPercent: 3, TempC: 40}}}, at)
	if p.CPU != 12.5 || p.MemTotal != 8 || len(p.GPUs) != 1 || p.GPUs[0].Index != 1 || p.GPUs[0].Temp != 40 {
		t.Fatalf("%+v", p)
	}
	if p := LivePoint(agentproto.Metrics{}, at); p.GPUs == nil {
		t.Fatal("gpus must be [] not null")
	}
}
