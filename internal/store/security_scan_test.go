package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestHeartbeatDurabilityBudgetSurvivesRestart(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(fmt.Sprint("checkpoint=", checkpoint), func(t *testing.T) {
			c := testkit.Config()
			now := time.Now().UTC()
			dir := t.TempDir()
			opts := Options{Observers: []string{"home", "cloud"}, Now: func() time.Time { return now }}
			s := openTest(t, dir, opts)
			b := batch(c, testkit.Observation(c, "home-http", 1, now, "ok"))
			b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: now}
			if err := s.Put(b); err != nil {
				t.Fatal(err)
			}
			if checkpoint {
				if err := s.Checkpoint(); err != nil {
					t.Fatal(err)
				}
			}
			s.Close()
			s = openTest(t, dir, opts)
			journal := func() int64 {
				st, err := os.Stat(filepath.Join(dir, "journal.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				return st.Size()
			}
			size := journal()
			for i := 1; i <= 100; i++ {
				// An observation already acknowledged is not new durable work.
				// Vary all sender-owned heartbeat fields, including receipt time
				// for direct store callers, without advancing the collector clock.
				b.Revision = fmt.Sprintf("%064x", i)
				b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: fmt.Sprint("boot", i), SampledAt: now.Add(time.Duration(i) * time.Millisecond), ReceivedAt: now.Add(-time.Hour)}
				if err := s.Put(b); !errors.Is(err, ErrHeartbeatRate) {
					t.Fatal("sender bypassed heartbeat budget", i, err)
				}
				if journal() != size {
					t.Fatal("rate limited heartbeat wrote journal")
				}
			}
			// Another observer can still write a heartbeat and observations.
			other := batch(c, testkit.Observation(c, "cloud-http", 1, now, "ok"))
			other.Heartbeat = &model.Heartbeat{Observer: "cloud", Boot: "boot", SampledAt: now}
			if err := s.Put(other); err != nil {
				t.Fatal("heartbeat flood blocked another observer", err)
			}
			// A new observation can carry a heartbeat without extra fsyncs.
			b.Observations = []model.Observation{testkit.Observation(c, "home-http", 2, now, "ok")}
			if err := s.Put(b); err != nil {
				t.Fatal("heartbeat budget blocked new observations", err)
			}
			b.Observations = nil
			now = now.Add(heartbeatWriteInterval - time.Nanosecond)
			b.Heartbeat.SampledAt = now
			if err := s.Put(b); !errors.Is(err, ErrHeartbeatRate) {
				t.Fatal("heartbeat admitted before cadence elapsed", err)
			}
			now = now.Add(time.Nanosecond)
			b.Heartbeat.SampledAt = now
			if err := s.Put(b); err != nil {
				t.Fatal("heartbeat not admitted at cadence boundary", err)
			}
			s.Close()
			s = openTest(t, dir, opts)
			_, beats := s.View()
			if h := beats["home"]; !h.SampledAt.Equal(now) || !h.ReceivedAt.Equal(now) || h.Revision != b.Revision {
				t.Fatal("latest admitted heartbeat not recovered", h)
			}
		})
	}
}

func TestObserverQuotasSurviveRestartAndReserveHeartbeats(t *testing.T) {
	for _, quota := range []string{"receipts", "bytes"} {
		t.Run(quota, func(t *testing.T) {
			c := testkit.Config()
			now := time.Now().UTC()
			dir := t.TempDir()
			opts := Options{Observers: []string{"home", "cloud"}, MaxReceipts: 4, MaxStateBytes: 32 << 10, Now: func() time.Time { return now }}
			if quota == "bytes" {
				opts.MaxReceipts = 1000
			}
			s := openTest(t, dir, opts)
			accepted := 0
			for i := 1; i < 30; i++ {
				v := testkit.Observation(c, "home-http", uint64(i), now, "ok")
				if quota == "bytes" {
					v.Facts["large"] = strings.Repeat("x", 6000)
				}
				err := s.Put(batch(c, v))
				if errors.Is(err, ErrScopeCapacity) {
					break
				}
				if err != nil {
					t.Fatal("global capacity reached first", err)
				}
				accepted++
			}
			if accepted == 0 || accepted >= 29 {
				t.Fatal("quota ineffective", accepted)
			}
			if err := s.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			s.Close()
			s = openTest(t, dir, opts)
			v := testkit.Observation(c, "home-http", 99, now, "ok")
			if quota == "bytes" {
				v.Facts["large"] = strings.Repeat("x", 6000)
			}
			if err := s.Put(batch(c, v)); !errors.Is(err, ErrScopeCapacity) {
				t.Fatal("restart reset scope accounting", err)
			}
			other := testkit.Observation(c, "cloud-http", 1, now, "ok")
			if err := s.Put(batch(c, other)); err != nil {
				t.Fatal("one observer blocked another", err)
			}
			for _, id := range opts.Observers {
				b := model.Batch{Schema: model.BatchSchema, Project: c.Project, Heartbeat: &model.Heartbeat{Observer: id, Boot: "new-boot", SampledAt: now}}
				if err := s.Put(b); err != nil {
					t.Fatal("full observations blocked heartbeat", err)
				}
			}
		})
	}
}

func TestStateViewDetachesLatestEvidenceAndRetainsEvaluationTail(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{MaxHistory: 20})
	for i := 1; i <= 60; i++ {
		v := testkit.Observation(c, "home-http", uint64(i), now.Add(time.Duration(i)*time.Millisecond), "ok")
		v.Facts["large"] = strings.Repeat("x", 2048)
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, _ := s.StateView()
	if len(rows) != 40 {
		t.Fatal("state read included full history", len(rows))
	}
	for i := range rows {
		if rows[i].Sequence == 60 {
			rows[i].Facts["response_proof"] = "changed"
		}
	}
	rows2, _, _ := s.StateView()
	for _, v := range rows2 {
		if v.Facts["response_proof"] == "changed" {
			t.Fatal("state view aliased durable evidence")
		}
	}
	full := s.History("home-http", map[string]string{"home-http": rows[0].Contract}, "", 1)
	if len(full) != 1 || len(full[0].Facts["large"]) != 2048 {
		t.Fatal("summary modified full history")
	}
}

func TestHistoryBoundsBytesWithoutTruncatingIndividualEvidence(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{})
	for i := 1; i <= 20; i++ {
		v := testkit.Observation(c, "home-http", uint64(i), now, "ok")
		v.Facts["large"] = strings.Repeat("&", 20000)
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	rows := s.History("home-http", config.Contracts(c), config.Revision(c), 500)
	if len(rows) == 0 || len(rows) >= 20 {
		t.Fatal("history byte limit not applied", len(rows))
	}
	raw, _ := json.Marshal(rows)
	if len(raw) > 1<<20 {
		t.Fatal("history too large", len(raw))
	}
	for _, v := range rows {
		if len(v.Facts["large"]) != 20000 {
			t.Fatal("history altered diagnostic value")
		}
	}
}

func BenchmarkStateViewLargeStore(b *testing.B) {
	s, err := Open(b.TempDir(), Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	c := testkit.Config()
	now := time.Now().UTC()
	// About 100 MiB of serialized evidence and 40,000 retained observations.
	for check := 0; check < 1000; check++ {
		for i := 0; i < 40; i++ {
			v := testkit.Observation(c, "home-http", uint64(check*40+i+1), now, "ok")
			v.Check = fmt.Sprintf("check-%d", check)
			v.Facts["large"] = strings.Repeat("x", 2048)
			s.addRow(v)
		}
	}
	s.prune()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.StateView()
	}
}

func BenchmarkExpiredTailConflict(b *testing.B) {
	c := testkit.Config()
	now := time.Now().UTC()
	for _, count := range []int{1, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s, err := Open(b.TempDir(), Options{Now: func() time.Time { return now }})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { s.Close() })
			v := testkit.Observation(c, "home-http", 1, now, "ok")
			s.rowHashes[v.ID] = observationHash(v)
			for i := 1; i < count; i++ {
				s.rowHashes[fmt.Sprintf("other.%d", i)] = "hash"
			}
			v.Outcome = "fail"
			txn := batch(c, v)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := s.Put(txn); !errors.Is(err, ErrConflict) {
					b.Fatal(err)
				}
			}
		})
	}
}
