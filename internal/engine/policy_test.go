package engine

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestProviderSettleReloadUsesLegacySamples(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		window    int
		facts     map[string]string
		confirmed bool
	}{
		{"enable on old unmarked samples", 120, nil, false},
		{"shorter than publication age", 10, nil, true},
		{"disable old marker", 0, map[string]string{"provider_reference_settling": "true"}, true},
		{"shorten old marker", 10, map[string]string{"provider_reference_settling": "true"}, true},
		{"known mode policy", 120, map[string]string{"mode": "global"}, true},
		{"known TUN policy", 120, map[string]string{"tun_enabled": "false"}, true},
		{"unstable reference with fixed policy", 120, map[string]string{"provider_reference_consistency": "changed"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testkit.Config()
			x := &c.Checks[0]
			x.Kind, x.IntervalSeconds, x.FailureThreshold = "mihomo", 10, 2
			tun, zero := true, 0
			x.Adapter.ExpectedMode, x.Adapter.ExpectedTUN = "rule", &tun
			x.Adapter.ProviderReference = &model.MihomoProviderReference{URL: "https://publisher.invalid/members.json", Providers: []string{"pool"}, MaxAgeSeconds: 3600, SettleSeconds: &zero}
			var rows []model.Observation
			for i := 0; i < 2; i++ {
				o := testkit.Observation(c, x.ID, uint64(i+1), now.Add(time.Duration(i-1)*10*time.Second), "policy")
				o.Code = "provider_membership_mismatch"
				o.Facts = map[string]string{"provider_reference_published_at": now.Add(-20 * time.Second).Format(time.RFC3339Nano), "provider_reference_consistency": "stable", "mode": "rule", "tun_enabled": "true"}
				for key, value := range tc.facts {
					o.Facts[key] = value
				}
				rows = append(rows, o)
			}
			x.Adapter.ProviderReference.SettleSeconds = &tc.window
			if !config.Counted(rows[0], config.Contracts(c), config.Revision(c)) {
				t.Fatal("threshold reload discarded old samples")
			}
			if got := Evaluate(*x, rows, now); got.PolicyConfirmed != tc.confirmed {
				t.Fatalf("legacy sample used its old window: %+v", got)
			}
			var carry Carry
			carry.Fold(rows[0], time.Duration(x.StaleSeconds)*time.Second)
			if got := EvaluateCarried(*x, &carry, rows[1:], now); got.PolicyConfirmed != tc.confirmed {
				t.Fatalf("trimming changed the reloaded decision: %+v", got)
			}
		})
	}
}

func TestProviderSettleUsesLoadEvidenceAndSampleTime(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	x := testkit.Config().Checks[0]
	x.IntervalSeconds, x.FailureThreshold = 10, 2
	x.Adapter.ProviderReference = &model.MihomoProviderReference{MaxAgeSeconds: 3600}
	var rows []model.Observation
	for i := 0; i < 13; i++ {
		at := now.Add(time.Duration(i-12) * 10 * time.Second)
		// Each sample sees a fresh publication. The independent policy run
		// must still confirm after 120 seconds instead of restarting.
		rows = append(rows, model.Observation{SampledAt: at, Outcome: "policy", Code: "provider_membership_mismatch", Facts: map[string]string{
			"provider_reference_published_at": at.Add(-20 * time.Second).Format(time.RFC3339Nano),
			"provider_reference_load_pending": "true",
		}})
	}
	if got := Evaluate(x, rows, now); !got.PolicyConfirmed {
		t.Fatalf("republications postponed a sustained policy: %+v", got)
	}
	// Evaluating a retained sample later cannot make its publication older
	// at measurement time. Keep the sample fresh so only settling decides.
	rows = rows[len(rows)-2:]
	if got := Evaluate(x, rows, now.Add(30*time.Second)); got.PolicyConfirmed {
		t.Fatalf("wall clock advanced a ten-second policy run: %+v", got)
	}
	// A loaded provider or independent fixed policy bypasses extra waiting,
	// even if a previous worker marker said it was settling.
	rows[1].Facts["provider_reference_load_pending"] = "false"
	rows[1].Facts["provider_reference_settling"] = "true"
	if got := Evaluate(x, rows, now); !got.PolicyConfirmed {
		t.Fatalf("known drift was delayed by the old marker: %+v", got)
	}
}
