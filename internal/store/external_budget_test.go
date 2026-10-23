package store

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestExternalReplayCapacityPreflight(t *testing.T) {
	c := externalStoreConfig(t)
	for _, scoped := range []bool{false, true} {
		o := Options{MaxStateBytes: 128 << 10}.WithTopology(c)
		if scoped {
			o.MaxStateBytes = 1 << 20
			o.Observers = []string{"home", "cloud"}
			o.ObserverWeights = map[string]int{"home": 1, "cloud": 10}
		}
		_, err := Budgets(o)
		if !errors.Is(err, ErrCapacity) && !errors.Is(err, ErrScopeCapacity) {
			t.Fatalf("impossible replay window accepted: %v", err)
		}
	}
	s, err := Open(t.TempDir(), Options{MaxStateBytes: 512 << 10}.WithTopology(c))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	next := config.Clone(c)
	for i := 0; i < 2; i++ {
		x := next.Checks[len(next.Checks)-1]
		x.ID += string(rune('a' + i))
		next.Checks = append(next.Checks, x)
	}
	if err := s.ValidateTopology(next); !errors.Is(err, ErrScopeCapacity) && !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized reload accepted: %v", err)
	}
	if len(s.options.Bindings) != 1 {
		t.Fatal("preflight mutated live bindings")
	}
}

func TestExternalBudgetErrorsRemainCapacityErrors(t *testing.T) {
	c := externalStoreConfig(t)
	s, err := Open(t.TempDir(), Options{}.WithTopology(c))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := s.options.Now()
	o := externalStoreObservation(t, c, "sample", 25, 1, now, now)
	s.externalBytes = maxExternalStateBytes
	_, err = s.mergeExternalLocked([]model.Observation{o})
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("lost capacity classification: %v", err)
	}
}
