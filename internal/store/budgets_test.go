package store

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
	"time"
)

func TestWeightedQuotaIsEnforcedIndependently(t *testing.T) {
	c := testkit.Config()
	s := openTest(t, t.TempDir(), Options{Observers: []string{"home", "cloud"}, ObserverWeights: map[string]int{"home": 3, "cloud": 1}, MaxReceipts: 120})
	put := func(observer string, start, n int) error {
		rows := []model.Observation{}
		for i := start; i < start+n; i++ {
			rows = append(rows, testkit.Observation(c, observer+"-http", uint64(i), time.Now(), "ok"))
		}
		return s.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: rows})
	}
	if err := put("home", 1, 85); err != nil {
		t.Fatal(err)
	}
	if err := put("cloud", 1, 30); err != nil {
		t.Fatal(err)
	}
	if err := put("cloud", 31, 1); !errors.Is(err, ErrScopeCapacity) {
		t.Fatalf("cloud exceeded reservation: %v", err)
	}
	if err := put("home", 86, 5); err != nil {
		t.Fatalf("another observer consumed home reservation: %v", err)
	}
	if err := put("home", 91, 1); !errors.Is(err, ErrScopeCapacity) {
		t.Fatal(err)
	}
	u := s.Usage()
	if u.Observers[0].ReceiptLimit != 30 || u.Observers[1].ReceiptLimit != 90 {
		t.Fatal(u)
	}
}
func TestBudgetValidationAndRounding(t *testing.T) {
	for _, weights := range []map[string]int{{"home": -1}, {"home": 1001}, {"other": 1}} {
		if _, e := Budgets(Options{Observers: []string{"home"}, ObserverWeights: weights}); e == nil {
			t.Fatal(weights)
		}
	}
	u, e := Budgets(Options{Observers: []string{"a", "b", "c"}, MaxReceipts: 100, ObserverWeights: map[string]int{"a": 7}})
	if e != nil {
		t.Fatal(e)
	}
	var count int
	var bytes int64
	for _, o := range u.Observers {
		count += o.ReceiptLimit
		bytes += o.ByteLimit
	}
	if count > u.MaxReceipts || bytes+1024+3*2048 > u.MaxStateBytes {
		t.Fatal(u)
	}
}
