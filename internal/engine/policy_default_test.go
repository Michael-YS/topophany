package engine

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestOrdinaryPolicyDefaultIsImmediateAndIndependent(t *testing.T) {
	for _, kind := range []string{"http", "host", "wireguard", "mihomo"} {
		t.Run(kind, func(t *testing.T) {
			c := testkit.Config()
			x := c.Checks[0]
			x.Kind, x.FailureThreshold = kind, 20
			now := time.Now().UTC()
			o := testkit.Observation(c, x.ID, 1, now, "policy")
			if got := Evaluate(x, []model.Observation{o}, now); !got.PolicyConfirmed || got.Health != "degraded" {
				t.Fatalf("legacy policy was delayed by failure threshold: %+v", got)
			}
			x.PolicyThreshold = 2
			if got := Evaluate(x, []model.Observation{o}, now); got.PolicyConfirmed {
				t.Fatal("explicit confirmation ignored", got)
			}
			var carry Carry
			carry.Fold(o, time.Duration(x.StaleSeconds)*time.Second)
			p := testkit.Observation(c, x.ID, 2, now.Add(time.Second), "policy")
			if got := EvaluateCarried(x, &carry, []model.Observation{p}, p.SampledAt); !got.PolicyConfirmed {
				t.Fatal("restart lost the policy run", got)
			}
			x.PolicyThreshold = 3
			if got := EvaluateCarried(x, &carry, []model.Observation{p}, p.SampledAt); got.PolicyConfirmed {
				t.Fatal("reloaded threshold ignored", got)
			}
			x.PolicyThreshold = 0
			if got := EvaluateCarried(x, &carry, []model.Observation{p}, p.SampledAt); !got.PolicyConfirmed {
				t.Fatal("reload failed to restore immediate confirmation", got)
			}
		})
	}
}

func TestExplicitPolicyConfirmationResetsOnMissingEvidence(t *testing.T) {
	for _, reset := range []string{"ok", "fail", "error", "unknown", "stale gap"} {
		t.Run(reset, func(t *testing.T) {
			c := testkit.Config()
			x := c.Checks[0]
			x.PolicyThreshold = 2
			now := time.Now().UTC()
			a := testkit.Observation(c, x.ID, 1, now, "policy")
			b := testkit.Observation(c, x.ID, 2, now.Add(time.Second), reset)
			at := now.Add(2 * time.Second)
			rows := []model.Observation{a, b}
			if reset == "stale gap" {
				rows = rows[:1]
				at = now.Add(time.Duration(x.StaleSeconds+1) * time.Second)
			}
			rows = append(rows, testkit.Observation(c, x.ID, 3, at, "policy"))
			if got := Evaluate(x, rows, at); got.PolicyConfirmed {
				t.Fatal("old policy samples crossed an evidence boundary", got)
			}
		})
	}
}
