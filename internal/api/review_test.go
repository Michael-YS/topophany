package api

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestObserverIdentitySeparatorCannotCrossScopes(t *testing.T) {
	s, _, b := fixture(t)
	s.Config.Observers = append(s.Config.Observers, model.Observer{ID: "home.branch", Context: "test", StaleSeconds: 90})
	x := s.Config.Checks[0]
	x.ID, x.Observer = "branch-http", "home.branch"
	s.Config.Checks = append(s.Config.Checks, x)
	b.Revision = config.Revision(s.Config)
	legitimate := testkit.Observation(s.Config, x.ID, 1, s.Now(), "ok")
	forged := testkit.Observation(s.Config, "home-http", 1, s.Now(), "ok")
	forged.Boot = "branch.boot"
	forged.ID = legitimate.ID
	b.Observations = []model.Observation{forged}
	if code, _ := s.ValidateBatch(b, "home"); code != "invalid_observation_identity" {
		t.Fatalf("cross-scope collision accepted: %q", code)
	}
	b.Observations = []model.Observation{legitimate}
	if code, _ := s.ValidateBatch(b, "home.branch"); code != "" {
		t.Fatal("dotted observer rejected", code)
	}
}

func TestAllDiagnosticFieldsRejectTerminalControls(t *testing.T) {
	for _, field := range []string{"fact_key", "timing_key", "path"} {
		t.Run(field, func(t *testing.T) {
			s, _, b := fixture(t)
			v := &b.Observations[0]
			switch field {
			case "fact_key":
				v.Facts["\x1b]52;c;payload\a"] = "value"
			case "timing_key":
				v.Timings = map[string]int64{"\x1b[2J": 1}
			case "path":
				v.PathEvidence = "\x1b[2J"
			}
			if code, _ := s.ValidateBatch(b, "home"); code == "" {
				t.Fatal("unsafe diagnostic accepted")
			}
		})
	}
}

func TestJSONMediaTypeMustMatchExactly(t *testing.T) {
	_, h, b := fixture(t)
	for _, ct := range []string{"application/jsonp", "application/json-malicious", "application/json; broken"} {
		if w := request(h, "POST", "/v1/observations", workerToken, ct, payload(t, b)); w.Code != 415 {
			t.Fatal(ct, w.Code)
		}
	}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json; charset=utf-8", payload(t, b)); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
}
