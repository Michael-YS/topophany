package config_test

import (
	. "github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
)

func TestApplicationScopeValidationAndContract(t *testing.T) {
	c := testkit.Config()
	before := Contract(c, c.Checks[0])
	c.Applications = []model.Application{{ID: "shop", Name: "商城"}}
	c.Services[0].Application = "shop"
	c.Services[0].Environment = "production"
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	if Contract(c, c.Checks[0]) != before {
		t.Fatal("organization changed measurement contract")
	}
	copy := Clone(c)
	copy.Applications[0].Name = "changed"
	if c.Applications[0].Name != "商城" {
		t.Fatal("application clone aliases original")
	}
	if Public(c).Services[0].Application != "shop" {
		t.Fatal("public topology lost scope")
	}
	for _, mutate := range []func(*model.Config){
		func(c *model.Config) { c.Services[0].Application = "missing" },
		func(c *model.Config) { c.Applications = append(c.Applications, c.Applications[0]) },
		func(c *model.Config) { c.Checks[0].Application = "shop" },
		func(c *model.Config) { c.Services[0].Environment = "bad env" },
	} {
		bad := Clone(c)
		mutate(&bad)
		if Validate(bad) == nil {
			t.Fatal("accepted invalid scope")
		}
	}
}
