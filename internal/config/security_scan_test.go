package config_test

import (
	"fmt"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestSecretTransportBinding(t *testing.T) {
	for _, kind := range []string{"http", "mihomo"} {
		for _, tc := range []struct {
			url, pin       string
			proxy, allowed bool
		}{
			{"http://203.0.113.1/", "", false, false},
			{"http://localhost/", "", false, false},
			{"http://127.0.0.1/", "", false, true},
			{"http://[::1]/", "", false, true},
			{"http://127.0.0.1/", "203.0.113.1", false, false},
			{"http://127.0.0.1/", "", true, false},
			{"https://203.0.113.1/", "", false, true},
		} {
			c := testkit.Config()
			x := &c.Checks[0]
			x.Kind = kind
			x.Target = tc.url
			x.ResolveIP = tc.pin
			if kind == "http" {
				x.HTTP.BearerEnv = "SECRET"
			} else {
				x.Adapter.TokenEnv = "SECRET"
			}
			if tc.proxy {
				c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: "127.0.0.1:1080"}}
			}
			if err := config.Validate(c); (err == nil) != tc.allowed {
				t.Fatalf("%s %s pin=%s proxy=%v: %v", kind, tc.url, tc.pin, tc.proxy, err)
			}
		}
	}
	// A target's query is sent as a credential, with no token named; a
	// Mihomo check sends none.
	for _, tc := range []struct {
		kind, url string
		allowed   bool
	}{
		{"http", "http://203.0.113.1/health?token=x", false},
		{"http", "http://localhost/?token=x", false},
		{"http", "http://127.0.0.1/?token=x", true},
		{"http", "https://203.0.113.1/?token=x", true},
		{"http", "http://203.0.113.1/health", true},
		{"mihomo", "http://203.0.113.1:9090/?token=x", true},
	} {
		c := testkit.Config()
		x := &c.Checks[0]
		x.Kind, x.Target = tc.kind, tc.url
		if err := config.Validate(c); (err == nil) != tc.allowed {
			t.Fatalf("%s %s: %v", tc.kind, tc.url, err)
		}
	}
	for _, kind := range []string{"http-connect", "socks5", "https-connect"} {
		for _, host := range []string{"127.0.0.1:1080", "localhost:1080", "203.0.113.1:1080"} {
			for _, index := range []int{0, 1} {
				p := model.Proxy{Kind: kind, Address: host, Username: "user", PasswordEnv: "SECRET"}
				allowed := kind == "https-connect" || index == 0 && host == "127.0.0.1:1080"
				if err := config.ProxyCredentials(p, index); (err == nil) != allowed {
					t.Fatal(kind, host, index, err)
				}
			}
		}
	}
}

func TestGraphDepthIsBoundedAcrossSharedDAGBranches(t *testing.T) {
	c := testkit.Config()
	for i := 0; i < 40; i++ {
		n := model.Node{ID: fmt.Sprintf("deep-%d", i)}
		if i > 0 {
			n.Parent = fmt.Sprintf("deep-%d", i-1)
		}
		c.Nodes = append(c.Nodes, n)
	}
	for i := 0; i < 50; i++ {
		if err := config.Validate(c); err == nil {
			t.Fatal("deep graph accepted depending on map iteration order")
		}
	}
}
