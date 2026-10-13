package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestProviderEvidenceBudgetValidatedAtLoad(t *testing.T) {
	for _, mode := range []string{"count", "key", "control"} {
		c := testkit.Config()
		x := &c.Checks[0]
		x.Kind = "mihomo"
		x.Adapter.ExpectedProviders = map[string][]string{}
		switch mode {
		case "count":
			for i := 0; i < 17; i++ {
				x.Adapter.ExpectedProviders[fmt.Sprint(i)] = nil
			}
		case "key":
			x.Adapter.ExpectedProviders[strings.Repeat("x", 120)] = nil
		case "control":
			x.Adapter.ExpectedProviders["bad\x1bname"] = nil
		}
		if e := config.Validate(c); e == nil {
			t.Fatal("invalid provider config accepted", mode)
		}
	}
}

func TestHostPortRejectsControlDelimiters(t *testing.T) {
	for _, host := range []string{"example\t.invalid:80", "example\x7f.invalid:80", "example\v.invalid:80"} {
		if e := config.HostPort(host); e == nil {
			t.Fatal("unsafe host accepted", host)
		}
	}
}
