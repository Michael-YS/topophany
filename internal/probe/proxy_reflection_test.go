package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Exercise the real route dialers and both observation producers. TLS to the
// final peer authenticates its response; TLS only to a proxy does not.
func TestProxyLoginResponseReflection(t *testing.T) {
	const login = "private-login"
	for _, route := range []string{"public", "connect-user", "https-connect-user", "socks-user", "password", "ssh-user", "later-hop"} {
		t.Run(route, func(t *testing.T) {
			c := testkit.Config()
			switch route {
			case "public":
				c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: connectFixture(t, "", "", nil)}}
			case "connect-user":
				c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: connectFixture(t, login, "", nil), Username: login}}
			case "https-connect-user", "later-hop":
				relay := connectHandler(t)
				proxy, ca := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Proxy-Authorization") == "" {
						t.Error("proxy login was not sent")
					}
					relay.ServeHTTP(w, r)
				}))
				c.Paths[0].Proxies = []model.Proxy{{Kind: "https-connect", Address: strings.TrimPrefix(proxy.URL, "https://"), Username: login, CAFile: ca}}
				if route == "later-hop" {
					c.Paths[0].Proxies = append([]model.Proxy{{Kind: "http-connect", Address: connectFixture(t, "", "", nil)}}, c.Paths[0].Proxies...)
				}
			case "socks-user":
				c.Paths[0].Proxies = []model.Proxy{{Kind: "socks5", Address: socksFixture(t, login, "", nil), Username: login}}
			case "password":
				t.Setenv("REFLECTION_PASSWORD", login)
				c.Paths[0].Proxies = []model.Proxy{{Kind: "http-connect", Address: connectFixture(t, "", login, nil), PasswordEnv: "REFLECTION_PASSWORD"}}
			case "ssh-user":
				p, _ := sshFixture(t, login)
				c.Paths[0].Proxies = []model.Proxy{p}
			}
			for _, encrypted := range []bool{false, true} {
				t.Run(map[bool]string{false: "http", true: "https"}[encrypted], func(t *testing.T) {
					h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/configs":
							io.WriteString(w, `{"mode":"`+login+`"}`)
						case "/proxies":
							io.WriteString(w, `{"proxies":{"root":{"type":"Selector","now":"`+login+`","testUrl":"https://`+login+`.invalid/"},"`+login+`":{"type":"VLESS"}}}`)
						case "/providers/proxies":
							io.WriteString(w, `{"providers":{"airport":{"proxies":[{"name":"node-a"},{"name":"`+login+`"}]}}}`)
						default:
							w.Header().Set("X-Vercel-Id", login+"::"+login+"::x")
							io.WriteString(w, "192.0.2.1")
						}
					})
					var s *httptest.Server
					ca := ""
					if encrypted {
						s, ca = tlsFixture(t, h)
					} else {
						s = httptest.NewServer(h)
						defer s.Close()
					}
					secret := route != "public" && !encrypted
					x := httpCheck(s.URL)
					if route == "ssh-user" {
						// This checks secret reflection, not helper startup speed.
						// Race-instrumented subprocesses need headroom when the
						// whole suite is running packages in parallel.
						x.TimeoutSeconds = 5
					}
					x.Path = "direct"
					x.CAFile = ca
					x.HTTP.ExpectedExit = []string{"192.0.2.1"}
					o := (Runner{}).Run(context.Background(), c, x)
					if o.Outcome != "ok" || o.Facts["edge_provider"] != "vercel" || (o.Facts["peer_text"] == withheld) != secret {
						t.Fatal(o)
					}
					if secret {
						if o.Facts["edge_pop"] != "" || o.Facts["edge_region"] != "" || o.Facts["exit_ip"] != "" {
							t.Fatal("private HTTP evidence retained", o)
						}
					} else if o.Facts["edge_pop"] != login || o.Facts["exit_ip"] != "192.0.2.1" {
						t.Fatal("public HTTP evidence lost", o)
					}
					x.Kind = "mihomo"
					x.Adapter = model.AdapterOptions{RootProxy: "root", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"airport": {"node-a", "node-b"}}}
					o = (Runner{}).Run(context.Background(), c, x)
					if o.Outcome != "policy" || (o.Facts["peer_text"] == withheld) != secret {
						t.Fatal(o)
					}
					if secret {
						b, _ := json.Marshal(o)
						if strings.Contains(string(b), login) {
							t.Fatalf("private login leaked: %s", b)
						}
						if o.Facts["mode"] != "other" || o.Facts["reported_chain"] != "undeclared" || o.Facts["reported_selection"] != "undeclared" || o.Facts["provider.airport"] != "node-a,undeclared" || o.Facts["test_url"] != "" {
							t.Fatal(o)
						}
					} else if o.Facts["mode"] != login || o.Facts["reported_selection"] != login || o.Facts["test_url"] == "" {
						t.Fatal("public adapter evidence lost", o)
					}
				})
			}
		})
	}
}
