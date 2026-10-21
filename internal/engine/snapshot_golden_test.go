package engine

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Captured from a056b9a, before Prepared existed. Each line hashes fifteen
// newline-terminated snapshots: five evaluation times, each in three views.
// Only Version is normalized. Do not regenerate these from the implementation
// under test: a shared judgment bug would then change both sides of the test.
//
//go:embed testdata/snapshot-a056b9a.sha256
var snapshotGolden string

func TestSnapshotPathsMatchPreOptimizationGolden(t *testing.T) {
	want := strings.Fields(snapshotGolden)
	if len(want) != 120 {
		t.Fatal("expected 120 frozen scenario digests")
	}
	random := rand.New(rand.NewPCG(17, 91))
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	outcomes := []string{"ok", "fail", "error", "unknown", "policy"}
	for scenario := range want {
		c := testkit.Config()
		for i := range c.Checks {
			x := &c.Checks[i]
			x.Disabled = random.IntN(20) == 0
			x.Maintenance = random.IntN(20) == 0
			x.FailureThreshold = 1 + random.IntN(5)
			x.RecoveryThreshold = 1 + random.IntN(4)
		}
		var rows []model.Observation
		var carries []Carry
		for _, x := range c.Checks {
			var carry Carry
			for sample := 0; sample < 60; sample++ {
				v := testkit.Observation(c, x.ID, uint64(sample+1), start.Add(time.Duration(sample-45)*time.Second), outcomes[random.IntN(len(outcomes))])
				v.Code = "policy_test"
				if sample < 20 {
					carry.Fold(v, time.Duration(x.StaleSeconds)*time.Second)
				} else {
					rows = append(rows, v)
				}
			}
			carries = append(carries, carry)
		}
		beats := map[string]model.Heartbeat{}
		for _, observer := range c.Observers {
			beats[observer.ID] = model.Heartbeat{Project: c.Project, Observer: observer.ID, Boot: fmt.Sprint(scenario), SampledAt: start.Add(time.Duration(random.IntN(60)-30) * time.Second), Revision: config.Revision(c)}
		}
		plan := NewPlan(c)
		prepared := plan.Prepare(rows, carries, beats)
		for _, mode := range []string{"one-shot", "prepared"} {
			t.Run(fmt.Sprintf("%03d/%s", scenario, mode), func(t *testing.T) {
				digest := sha256.New()
				encoder := json.NewEncoder(digest)
				for _, offset := range []time.Duration{-time.Hour, -10 * time.Second, 0, 10 * time.Second, 24 * time.Hour} {
					for _, view := range []string{"", "home", "cloud"} {
						var snap model.Snapshot
						if mode == "prepared" {
							snap = prepared.Build("review", view, start.Add(offset))
						} else {
							snap = plan.Build("review", view, rows, carries, beats, start.Add(offset))
						}
						snap.Version = "fixture"
						if err := encoder.Encode(snap); err != nil {
							t.Fatal(err)
						}
					}
				}
				if got := hex.EncodeToString(digest.Sum(nil)); got != want[scenario] {
					t.Fatalf("snapshot semantics changed from a056b9a: got %s want %s", got, want[scenario])
				}
			})
		}
	}
}
