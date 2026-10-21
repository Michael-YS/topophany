package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func confirmedPolicyAfterGap(t *testing.T) (*Store, model.Config, Options, time.Time) {
	t.Helper()
	c := externalStoreConfig(t)
	limit := 80.0
	x := &c.Checks[len(c.Checks)-1]
	x.PolicyThreshold = 2
	x.External.Assertions = &model.ResourceAssertions{CPUPercentMax: &limit}
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-150 * time.Second)
	now := at.Add(150 * time.Second)
	opts := externalStoreOptions(c, &now)
	s := openTest(t, t.TempDir(), opts)
	for i, gap := range []time.Duration{0, 10 * time.Second, 2 * time.Minute} {
		stamp := at.Add(gap)
		v := externalStoreObservation(t, c, fmt.Sprint(i), 90, uint64(i+1), stamp, stamp)
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	state := s.ExternalView().Bindings["vm-resources"]
	if !state.PolicyConfirmed || state.PolicyRun != 1 || state.ReportSamples != 3 {
		t.Fatalf("policy gap setup: %+v", state)
	}
	return s, c, opts, at
}

func TestRejectedReportsPreserveConfirmedPolicyAfterGap(t *testing.T) {
	for _, kind := range []string{"duplicate", "conflict", "late", "unusable"} {
		t.Run(kind, func(t *testing.T) {
			s, c, _, at := confirmedPolicyAfterGap(t)
			before := s.ExternalView().Bindings["vm-resources"]
			v := externalStoreObservation(t, c, "2", 90, 4, at.Add(150*time.Second), at.Add(2*time.Minute))
			switch kind {
			case "conflict":
				v.External.Report.Resource.CPU.Basis = "conflicting_basis"
			case "late":
				v.External.Report.SampleID = "older"
				v.External.Report.ObservedAt = at.Add(-time.Second)
			case "unusable":
				v.External.Report.TimeBasis = model.TimeBasisUnknown
			}
			if err := s.Put(batch(c, v)); err != nil {
				t.Fatal(err)
			}
			after := s.ExternalView().Bindings["vm-resources"]
			if after.LastResult != kind || after.PolicyConfirmed != before.PolicyConfirmed || after.PolicyRun != before.PolicyRun || after.ReportSamples != before.ReportSamples {
				t.Fatalf("%s changed accepted confirmation: %+v", kind, after)
			}
		})
	}
}

func TestPolicyConfirmationSurvivesRestartAndUnchangedRules(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(fmt.Sprint(checkpoint), func(t *testing.T) {
			s, c, opts, _ := confirmedPolicyAfterGap(t)
			if checkpoint {
				if err := s.Checkpoint(); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, s.dir, opts)
			if state := s.ExternalView().Bindings["vm-resources"]; !state.PolicyConfirmed || state.PolicyRun != 1 {
				t.Fatalf("restart changed confirmation: %+v", state)
			}
			// Unrelated confirmation and display edits are not policy edits.
			x := &c.Checks[len(c.Checks)-1]
			x.FailureThreshold++
			x.Notes = "display edit"
			s.SetExternalContext(c)
			s.SetCurrent(config.Revision(c), config.Contracts(c), config.Stale(c))
			if !s.ExternalView().Bindings["vm-resources"].PolicyConfirmed {
				t.Fatal("unrelated reload changed policy confirmation")
			}
			// An explicit policy threshold edit does rejudge the stored run.
			x.PolicyThreshold = 3
			s.SetExternalContext(c)
			if s.ExternalView().Bindings["vm-resources"].PolicyConfirmed {
				t.Fatal("changed threshold did not rejudge the current run")
			}
			x.PolicyThreshold = 1
			s.SetExternalContext(c)
			if !s.ExternalView().Bindings["vm-resources"].PolicyConfirmed {
				t.Fatal("lower threshold did not rejudge the current run")
			}
		})
	}
}
