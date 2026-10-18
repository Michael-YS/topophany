package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestPolicyThresholdValidationAndContract(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 20, 21} {
		c := testkit.Config()
		c.Checks[0].PolicyThreshold = n
		err := config.Validate(c)
		if (err == nil) != (n >= 0 && n <= 20) {
			t.Fatalf("threshold %d: %v", n, err)
		}
	}
	c := testkit.Config()
	before := config.Contracts(c)
	rev := config.Revision(c)
	config.Defaults(&c)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if c.Checks[0].PolicyThreshold != 0 || strings.Contains(string(raw), `"policy_threshold"`) {
		t.Fatal("default added a new field to old topologies")
	}
	c.Checks[0].PolicyThreshold = 3
	if config.Contracts(c)[c.Checks[0].ID] != before[c.Checks[0].ID] || config.Revision(c) == rev {
		t.Fatal("policy confirmation changed the measurement contract or missed revision")
	}
	for _, copy := range []model.Config{config.Clone(c), config.Public(c)} {
		if copy.Checks[0].PolicyThreshold != 3 {
			t.Fatal("policy option lost in clone/public")
		}
	}
}
