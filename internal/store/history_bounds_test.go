package store

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestHistoryCountsSurviveQuotaChangesLateSamplesAndRestart(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	dir := t.TempDir()
	s := openTest(t, dir, Options{MaxHistory: 10, Retention: time.Hour, Now: func() time.Time { return now }})
	check := func() {
		t.Helper()
		counts := map[string]int{}
		for _, row := range s.rows {
			counts[row.Observer]++
		}
		if !maps.Equal(counts, s.historyCounts) {
			t.Fatal("history accounting differs", counts, s.historyCounts)
		}
		checkAccounting(t, s)
	}
	for i := range 30 {
		id := "home-http"
		if i%3 == 0 {
			id = "cloud-http"
		}
		v := testkit.Observation(c, id, uint64(i+1), now.Add(-time.Duration(i%7)*time.Second), "ok")
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
		check()
	}
	s.SetAllocation([]string{"home", "cloud"}, map[string]int{"home": 3, "cloud": 1})
	s.prune()
	check()
	if s.historyCounts["cloud"] > 2 || s.historyCounts["home"] > 7 {
		t.Fatal("weighted history quota exceeded", s.historyCounts)
	}
	s.SetObservers(nil)
	s.prune()
	check()
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	opts := s.options
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, opts)
	check()
	now = now.Add(2 * time.Hour)
	s.prune()
	check()
	if len(s.rows) != 0 || len(s.historyCounts) != 0 {
		t.Fatal("expired history retained", len(s.rows), s.historyCounts)
	}
}

func TestCheckpointRejectsDuplicateFieldsAndStructuralBudgets(t *testing.T) {
	valid := `{"schema":1,"seen":{},"beats":{}}`
	fields := []string{`"schema":1`, `"seen":{}`, `"beats":{}`}
	for i := range 129 {
		fields = append(fields, fmt.Sprintf(`"extra%d":0`, i))
	}
	for _, tc := range []struct{ name, state, outer, want string }{
		{"state duplicate", `{"schema":1,"schema":1,"seen":{},"beats":{}}`, "", "duplicate state field"},
		{"state fields", "{" + strings.Join(fields, ",") + "}", "", "state field"},
		{"state nesting", `{"schema":1,"seen":{},"beats":{},"future":` + strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66) + "}", "", "nesting limit"},
		{"outer duplicate", valid, `,"sha256":"duplicate"`, "duplicate checkpoint field"},
		{"outer fields", valid, "," + strings.Join(fields[3:], ","), "checkpoint field"},
		{"outer nesting", valid, `,"future":` + strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), "nesting limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			raw := `{"sha256":"` + hashBytes([]byte(tc.state)) + `","payload":` + tc.state + tc.outer + "}"
			if err := os.WriteFile(filepath.Join(dir, "checkpoint.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(dir, Options{})
			if err == nil {
				s.Close()
				t.Fatal("invalid checkpoint accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wrong rejection: %v; want %q", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "journal.jsonl")); !os.IsNotExist(err) {
				t.Fatal("invalid checkpoint opened journal", err)
			}
		})
	}
}

func TestStateViewSizesSparseTailsWithoutLosingLegacyHistory(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	s := openTest(t, t.TempDir(), Options{Revision: config.Revision(c), Contracts: config.Contracts(c)})
	current := testkit.Observation(c, "home-http", 1, now, "ok")
	legacy := testkit.Observation(c, "cloud-http", 2, now, "ok")
	legacy.Contract, legacy.Revision = "", "old"
	if err := s.Put(batch(c, current, legacy)); err != nil {
		t.Fatal(err)
	}
	rows, _, _ := s.StateView()
	if len(rows) != 2 || cap(rows) > 3 {
		t.Fatal("sparse tails overallocated or history lost", len(rows), cap(rows))
	}
}
