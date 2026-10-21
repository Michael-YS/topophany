package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/testkit"
)

type syntheticSource struct {
	t       testing.TB
	server  *httptest.Server
	mu      sync.Mutex
	counts  map[string]int
	base    time.Time
	counter atomic.Int32
	list    atomic.Pointer[testkit.SyntheticListPayload]
	status  atomic.Pointer[testkit.SyntheticStatusPayload]
}

func newSyntheticSource(t testing.TB) *syntheticSource {
	t.Helper()
	s := &syntheticSource{t: t, counts: map[string]int{}, base: time.Now().UTC()}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	t.Setenv("TOPO_SYNTHETIC_SOURCE_TOKEN", "synthetic-source-token")
	s.setList(s.defaultList())
	s.setStatus(s.defaultStatus())
	return s
}

func (s *syntheticSource) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[path]
}

func (s *syntheticSource) bump(path string) {
	s.mu.Lock()
	s.counts[path]++
	s.mu.Unlock()
}

func (s *syntheticSource) setList(p testkit.SyntheticListPayload)     { s.list.Store(&p) }
func (s *syntheticSource) setStatus(p testkit.SyntheticStatusPayload) { s.status.Store(&p) }

func (s *syntheticSource) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer synthetic-source-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/list":
		s.bump("/list")
		_ = json.NewEncoder(w).Encode(s.list.Load())
	case "/counter":
		s.bump("/counter")
		n := s.counter.Add(1)
		p := testkit.SyntheticCounterPayload{Epoch: "counter-1", Sequence: uint64(n), ObservedAt: s.base.Add(time.Duration(n-1) * time.Second), CPUs: 4, MemoryTotal: 8 << 30, MemoryUsed: 2 << 30}
		switch n {
		case 1:
			p.Busy, p.Total = 1000, 10000
		case 2:
			p.Busy, p.Total = 1500, 11000
		default:
			p.Epoch = "counter-2"
			p.Busy, p.Total = 100, 1000
		}
		_ = json.NewEncoder(w).Encode(p)
	case "/status":
		s.bump("/status")
		_ = json.NewEncoder(w).Encode(s.status.Load())
	case "/bad-json":
		s.bump("/bad-json")
		_, _ = w.Write([]byte("{"))
	case "/unauthorized":
		s.bump("/unauthorized")
		w.WriteHeader(http.StatusUnauthorized)
	case "/slow":
		s.bump("/slow")
		time.Sleep(2500 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(s.status.Load())
	default:
		http.NotFound(w, r)
	}
}

func (s *syntheticSource) defaultList() testkit.SyntheticListPayload {
	now := s.base.UTC()
	f := func(v float64) *float64 { return &v }
	i := func(v int) *int { return &v }
	i64 := func(v int64) *int64 { return &v }
	memTotal, memUsed := uint64(8<<30), uint64(2<<30)
	rootTotal, rootUsed := uint64(100<<30), uint64(25<<30)
	dataTotal, dataUsed := uint64(300<<30), uint64(30<<30)
	return testkit.SyntheticListPayload{Epoch: "epoch-1", Machines: []testkit.SyntheticMachine{
		{ID: "vm-a", Samples: []testkit.SyntheticMachineSample{
			{SampleID: "vm-a-old", ObservedAt: now.Add(-60 * time.Second), CPUPercent: f(40), CPUs: i(4)},
			{SampleID: "vm-a-old", ObservedAt: now.Add(-60 * time.Second), CPUPercent: f(40), CPUs: i(4)},
			{SampleID: "vm-a-1", ObservedAt: now.Add(-10 * time.Second), CPUPercent: f(12.5), CPUs: i(4), CPUWindowMS: i64(250), CPUBasis: "guest_cpu", MemoryTotal: &memTotal, MemoryUsed: &memUsed, MemoryBasis: "available", Disks: []testkit.SyntheticDisk{
				{ID: "root", Path: "/", TotalBytes: &rootTotal, UsedBytes: &rootUsed, Percent: f(25), Basis: "free"},
				{ID: "data", Path: "/data", TotalBytes: &dataTotal, UsedBytes: &dataUsed, Percent: f(10), Basis: "free"},
			}},
		}},
		{ID: "vm-b", Samples: []testkit.SyntheticMachineSample{
			{SampleID: "vm-b-1", ObservedAt: now.Add(-5 * time.Second), CPUPercent: f(80), CPUs: i(8), Disks: []testkit.SyntheticDisk{{ID: "data", Path: "/data", TotalBytes: &dataTotal, UsedBytes: &dataUsed, Percent: f(10), Basis: "free"}}},
		}},
		{ID: "vm-unbound", Samples: []testkit.SyntheticMachineSample{{SampleID: "vm-u-1", ObservedAt: now.Add(-5 * time.Second), CPUPercent: f(1), CPUs: i(1)}}},
	}}
}

func (s *syntheticSource) defaultStatus() testkit.SyntheticStatusPayload {
	latency := int64(20)
	return testkit.SyntheticStatusPayload{Object: "svc-web", SampleID: "status-1", ObservedAt: s.base.UTC().Add(-5 * time.Second), TimeBasis: "upstream_sample", Status: "up", LatencyMS: &latency, ObservationPoint: "synthetic-edge"}
}

func (s *syntheticSource) URL(path string) string { return s.server.URL + path }
