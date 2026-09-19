// Package metrics is a server's metrics history: heartbeats folded into minute rows, the
// retention job that keeps the table bounded, and the bucketing behind the charts.
//
// Retention: minute rows (step 60) for 24 hours, then 15-minute rows (step 900) for 30 days.
// Compact runs from the ops service's retention loop in system scope.
package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

const (
	// MinuteRetention is how long minute rows are kept before they are folded into 15 minutes.
	MinuteRetention = 24 * time.Hour
	// Retention is how long anything is kept.
	Retention = 30 * 24 * time.Hour
	// MaxSparkPoints bounds each series of the home page's sparklines.
	MaxSparkPoints = 60
)

// Range is one chart window and the bucket it is drawn with (at most ~300 points).
type Range struct {
	Key  string
	Span time.Duration
	Step time.Duration
}

// Ranges are the windows the API accepts.
var Ranges = map[string]Range{
	"1h":  {"1h", time.Hour, time.Minute},              // 60 points
	"6h":  {"6h", 6 * time.Hour, 2 * time.Minute},      // 180
	"24h": {"24h", 24 * time.Hour, 5 * time.Minute},    // 288
	"7d":  {"7d", 7 * 24 * time.Hour, time.Hour},       // 168
	"30d": {"30d", 30 * 24 * time.Hour, 3 * time.Hour}, // 240
}

// RangeKeys is the enum, in order.
var RangeKeys = []string{"1h", "6h", "24h", "7d", "30d"}

// window is where a series of span, drawn in buckets of step, starts: aligned to step and
// ending with the current (partial) bucket, so it has exactly span/step buckets.
func window(now time.Time, span, step time.Duration) time.Time {
	s := int64(step / time.Second)
	end := now.Unix() / s * s // start of the current bucket
	return time.Unix(end-int64(span/time.Second)+s, 0).UTC()
}

// ---- recording ----------------------------------------------------------------------------------

// Minute accumulates one server's readings (heartbeats and the 5 s live readings) for one
// minute, so the history costs one write per server per minute. CPU, load and network are
// averaged; memory, disk and GPUs keep the latest reading (GPUs the latest non-empty one).
type Minute struct {
	At                       time.Time // the minute
	N                        int64
	cpu, load, rx, tx        float64
	memUsed, memTotal        uint64
	diskUsed, diskTotal      uint64
	gpus                     []agentproto.GPUMetric
}

// Add folds a reading taken at now into the minute. When now is in a later minute, the finished
// minute is returned (to be written) and accumulation starts over.
func (mm *Minute) Add(m agentproto.Metrics, now time.Time) (done *Minute) {
	minute := now.UTC().Truncate(time.Minute)
	if mm.N > 0 && !minute.Equal(mm.At) {
		c := *mm
		done = &c
		*mm = Minute{}
	}
	if mm.N == 0 {
		mm.At = minute
	}
	mm.N++
	mm.cpu += finite(m.CPUPercent)
	mm.load += finite(m.Load1)
	mm.rx += finite(m.NetRxBytesPerSec)
	mm.tx += finite(m.NetTxBytesPerSec)
	mm.memUsed, mm.memTotal, mm.diskUsed, mm.diskTotal = m.MemUsedBytes, m.MemTotalBytes, m.DiskUsedBytes, m.DiskTotalBytes
	if len(m.GPUs) > 0 {
		mm.gpus = m.GPUs
	}
	return done
}

// Record writes a finished minute (agent scope). A minute with no readings is a no-op.
func Record(ctx context.Context, q *db.Queries, serverID, teamID string, mm Minute) error {
	if mm.N == 0 {
		return nil
	}
	gpus := mm.gpus
	if gpus == nil {
		gpus = []agentproto.GPUMetric{}
	}
	g, _ := json.Marshal(gpus)
	n := float64(mm.N)
	return q.RecordServerMetric(ctx, db.RecordServerMetricParams{
		ServerID: serverID, TeamID: teamID, At: mm.At, Samples: mm.N,
		CpuPercent: round1(mm.cpu / n), MemUsed: clampI64(mm.memUsed), MemTotal: clampI64(mm.memTotal),
		DiskUsed: clampI64(mm.diskUsed), DiskTotal: clampI64(mm.diskTotal), Load1: round2(mm.load / n),
		NetRx: math.Round(mm.rx / n), NetTx: math.Round(mm.tx / n), Gpu: string(g),
	})
}

// LivePoint is one reading in the shape of a chart point (the server stream's "metrics" event).
func LivePoint(m agentproto.Metrics, at time.Time) Point {
	p := Point{At: at.UTC(), CPU: finite(m.CPUPercent), MemUsed: clampI64(m.MemUsedBytes), MemTotal: clampI64(m.MemTotalBytes),
		DiskUsed: clampI64(m.DiskUsedBytes), DiskTotal: clampI64(m.DiskTotalBytes), Load1: finite(m.Load1),
		NetRx: finite(m.NetRxBytesPerSec), NetTx: finite(m.NetTxBytesPerSec), GPUs: []GPUPoint{}}
	for _, g := range m.GPUs {
		p.GPUs = append(p.GPUs, GPUPoint{Index: g.Index, Util: g.UtilPercent, MemUsed: clampI64(g.MemUsedBytes), MemTotal: clampI64(g.MemTotalBytes), Temp: g.TempC})
	}
	return p
}

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

func clampI64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// ---- retention ----------------------------------------------------------------------------------

// Compact folds minute rows older than MinuteRetention into 15-minute rows and drops rows older
// than Retention. It is idempotent and safe to run concurrently with heartbeats.
func Compact(ctx context.Context, sqlDB *sql.DB, now time.Time) (compacted, pruned int64, err error) {
	err = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var e error
		if compacted, e = q.CompactServerMetrics(ctx, now.Add(-MinuteRetention)); e != nil {
			return fmt.Errorf("compact: %w", e)
		}
		if pruned, e = q.PruneServerMetrics(ctx, now.Add(-Retention)); e != nil {
			return fmt.Errorf("prune: %w", e)
		}
		return nil
	})
	return compacted, pruned, err
}

// ---- bucketing ----------------------------------------------------------------------------------

// GPUPoint is one GPU in a chart point.
type GPUPoint struct {
	Index    int     `json:"index"`
	Util     float64 `json:"util" doc:"Percent."`
	MemUsed  int64   `json:"mem_used" doc:"Bytes."`
	MemTotal int64   `json:"mem_total" doc:"Bytes."`
	Temp     float64 `json:"temp" doc:"Degrees Celsius."`
}

// Point is one bucket of a chart.
type Point struct {
	At        time.Time  `json:"at" doc:"Start of the bucket."`
	CPU       float64    `json:"cpu" doc:"Percent busy, averaged over the bucket."`
	MemUsed   int64      `json:"mem_used"`
	MemTotal  int64      `json:"mem_total"`
	DiskUsed  int64      `json:"disk_used"`
	DiskTotal int64      `json:"disk_total"`
	Load1     float64    `json:"load1"`
	NetRx     float64    `json:"net_rx" doc:"Bytes per second received."`
	NetTx     float64    `json:"net_tx" doc:"Bytes per second sent."`
	GPUs      []GPUPoint `json:"gpus"`
}

// Sample is one stored row, whatever its resolution.
type Sample struct {
	At                                     time.Time
	Samples                                int64
	CPU, Load1, NetRx, NetTx               float64
	MemUsed, MemTotal, DiskUsed, DiskTotal int64
	GPU                                    string // JSON array of agentproto.GPUMetric
}

type gpuAcc struct {
	w, util, mem, temp float64
	total              int64
}

type acc struct {
	at                  time.Time
	w                   float64
	cpu, load, rx, tx   float64
	mem, disk           float64
	n                   float64
	memTotal, diskTotal int64
	gpus                map[int]*gpuAcc
}

// Bucket groups samples into buckets of step starting at since. CPU, load, network and GPU
// figures are weighted by how many heartbeats each row stands for; totals are the maximum.
// Empty buckets are left out (a gap in the chart, not a zero).
func Bucket(samples []Sample, since time.Time, step time.Duration) []Point {
	s := int64(step / time.Second)
	byKey := map[int64]*acc{}
	for _, r := range samples {
		if r.At.Before(since) {
			continue
		}
		k := r.At.Unix() / s * s
		a := byKey[k]
		if a == nil {
			a = &acc{at: time.Unix(k, 0).UTC(), gpus: map[int]*gpuAcc{}}
			byKey[k] = a
		}
		w := float64(r.Samples)
		if w <= 0 {
			w = 1
		}
		a.w += w
		a.n++
		a.cpu += r.CPU * w
		a.load += r.Load1 * w
		a.rx += r.NetRx * w
		a.tx += r.NetTx * w
		a.mem += float64(r.MemUsed)
		a.disk += float64(r.DiskUsed)
		a.memTotal = max(a.memTotal, r.MemTotal)
		a.diskTotal = max(a.diskTotal, r.DiskTotal)
		var gs []agentproto.GPUMetric
		if r.GPU != "" && json.Unmarshal([]byte(r.GPU), &gs) == nil {
			for _, g := range gs {
				ga := a.gpus[g.Index]
				if ga == nil {
					ga = &gpuAcc{}
					a.gpus[g.Index] = ga
				}
				ga.w += w
				ga.util += g.UtilPercent * w
				ga.mem += float64(g.MemUsedBytes) * w
				ga.temp += g.TempC * w
				ga.total = max(ga.total, clampI64(g.MemTotalBytes))
			}
		}
	}
	keys := make([]int64, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]Point, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		p := Point{At: a.at, CPU: round1(a.cpu / a.w), Load1: round2(a.load / a.w), NetRx: math.Round(a.rx / a.w), NetTx: math.Round(a.tx / a.w),
			MemUsed: int64(a.mem / a.n), MemTotal: a.memTotal, DiskUsed: int64(a.disk / a.n), DiskTotal: a.diskTotal, GPUs: []GPUPoint{}}
		idx := make([]int, 0, len(a.gpus))
		for i := range a.gpus {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		for _, i := range idx {
			g := a.gpus[i]
			p.GPUs = append(p.GPUs, GPUPoint{Index: i, Util: round1(g.util / g.w), MemUsed: int64(g.mem / g.w), MemTotal: g.total, Temp: round1(g.temp / g.w)})
		}
		out = append(out, p)
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ---- queries ------------------------------------------------------------------------------------

// Series is GET …/servers/{server}/metrics.
type Series struct {
	Range       string  `json:"range" enum:"1h,6h,24h,7d,30d"`
	StepSeconds int     `json:"step_seconds"`
	Points      []Point `json:"points"`
}

// ServerSeries reads one server's chart for a range (team scope; the caller resolved the server).
func ServerSeries(ctx context.Context, q *db.Queries, teamID, serverID string, r Range, now time.Time) (Series, error) {
	since := window(now, r.Span, r.Step)
	rows, err := q.ServerMetricsSince(ctx, db.ServerMetricsSinceParams{ServerID: serverID, TeamID: teamID, Since: since})
	if err != nil {
		return Series{}, err
	}
	samples := make([]Sample, len(rows))
	for i, x := range rows {
		samples[i] = Sample{At: x.At, Samples: x.Samples, CPU: x.CpuPercent, Load1: x.Load1, NetRx: x.NetRx, NetTx: x.NetTx,
			MemUsed: x.MemUsed, MemTotal: x.MemTotal, DiskUsed: x.DiskUsed, DiskTotal: x.DiskTotal, GPU: x.Gpu}
	}
	return Series{Range: r.Key, StepSeconds: int(r.Step / time.Second), Points: Bucket(samples, since, r.Step)}, nil
}

// Spark is one server's sparklines: CPU percent, memory percent and mean GPU utilisation
// (null when the server reported no GPU in the window).
type Spark struct {
	CPU []float64 `json:"cpu"`
	Mem []float64 `json:"mem" doc:"Percent of memory in use."`
	GPU []float64 `json:"gpu" nullable:"true" doc:"Mean utilisation of the server's GPUs; null without a GPU."`
}

// Sparks is GET …/servers/metrics.
type Sparks struct {
	Range       string           `json:"range" enum:"1h,6h,24h,7d,30d"`
	StepSeconds int              `json:"step_seconds"`
	Servers     map[string]Spark `json:"servers"`
}

// SparkStep is the bucket of a range's sparklines (at most MaxSparkPoints).
func SparkStep(r Range) time.Duration { return r.Span / MaxSparkPoints }

// TeamSparks reads every server's sparklines in one query (team scope). Every server in
// serverIDs gets an entry, empty when it has no samples yet.
func TeamSparks(ctx context.Context, q *db.Queries, teamID string, serverIDs []string, r Range, now time.Time) (Sparks, error) {
	step := SparkStep(r)
	since := window(now, r.Span, step)
	rows, err := q.TeamServerMetricsSince(ctx, db.TeamServerMetricsSinceParams{TeamID: teamID, Since: since})
	if err != nil {
		return Sparks{}, err
	}
	by := map[string][]Sample{}
	for _, x := range rows {
		by[x.ServerID] = append(by[x.ServerID], Sample{At: x.At, Samples: x.Samples, CPU: x.CpuPercent, Load1: x.Load1, NetRx: x.NetRx, NetTx: x.NetTx,
			MemUsed: x.MemUsed, MemTotal: x.MemTotal, DiskUsed: x.DiskUsed, DiskTotal: x.DiskTotal, GPU: x.Gpu})
	}
	out := Sparks{Range: r.Key, StepSeconds: int(step / time.Second), Servers: map[string]Spark{}}
	for _, id := range serverIDs {
		out.Servers[id] = sparkOf(Bucket(by[id], since, step))
	}
	return out, nil
}

func sparkOf(points []Point) Spark {
	sp := Spark{CPU: []float64{}, Mem: []float64{}}
	var gpu []float64
	seenGPU := false
	for _, p := range points {
		sp.CPU = append(sp.CPU, p.CPU)
		mem := 0.0
		if p.MemTotal > 0 {
			mem = round1(float64(p.MemUsed) / float64(p.MemTotal) * 100)
		}
		sp.Mem = append(sp.Mem, mem)
		u := 0.0
		if len(p.GPUs) > 0 {
			seenGPU = true
			for _, g := range p.GPUs {
				u += g.Util
			}
			u = round1(u / float64(len(p.GPUs)))
		}
		gpu = append(gpu, u)
	}
	if seenGPU {
		sp.GPU = gpu
	}
	return sp
}
