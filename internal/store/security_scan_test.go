package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

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
	rows, _ := s.StateView()
	if len(rows) != 40 {
		t.Fatal("state read included full history", len(rows))
	}
	for i := range rows {
		if rows[i].Sequence == 60 {
			rows[i].Facts["response_proof"] = "changed"
		}
	}
	rows2, _ := s.StateView()
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

func TestStateViewPreservesLongRunningHysteresis(t *testing.T) {
	c := testkit.Config()
	x := c.Checks[0]
	x.FailureThreshold = 3
	x.RecoveryThreshold = 3
	c.Checks[0] = x
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{})
	for i := 0; i < 70; i++ {
		outcome := "fail"
		if i >= 3 && i%2 == 0 {
			outcome = "ok"
		}
		v := testkit.Observation(c, x.ID, uint64(i+1), now.Add(time.Duration(i)*time.Second), outcome)
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	full, _ := s.View()
	summary, _ := s.StateView()
	at := now.Add(70 * time.Second)
	a, z := engine.Evaluate(x, full, at), engine.Evaluate(x, summary, at)
	if a.Health != "down" || z.Health != a.Health || z.Since != a.Since {
		t.Fatal("summary lost established failure state", a, z)
	}
}
