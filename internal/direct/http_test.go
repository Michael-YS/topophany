package direct

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
)

func TestDirectHTTPRequiresPerSourceConsent(t *testing.T) {
	for _, adapter := range []string{"beszel", "komari"} {
		for _, target := range []string{"http://192.168.31.160:8090", "http://100.86.138.20:8090", "http://monitor.example.test/prefix"} {
			t.Run(adapter+"/"+target, func(t *testing.T) {
				s := SourceConfig{ID: "test", Name: "Test", Adapter: adapter, URL: target, Interval: 30, Timeout: 8, Stale: 180, MaxAge: 180}
				if err := s.Validate(); AsError(err).Code != "invalid_request" {
					t.Fatal("non-loopback HTTP accepted without consent", err)
				}
				s.AllowInsecure = true
				if err := s.Validate(); err != nil {
					t.Fatal("explicit HTTP consent refused", err)
				}
				for _, invalid := range []string{"ftp://monitor.example.test", "http://user:secret@monitor.example.test", "http://monitor.example.test?token=secret", "http://monitor.example.test/a/../b"} {
					s.URL = invalid
					if err := s.Validate(); err == nil {
						t.Fatal("HTTP consent bypassed URL validation", invalid)
					}
				}
			})
		}
	}
	for _, value := range []string{"true", "false", "null", `"true"`, "1"} {
		var s SourceConfig
		err := json.Unmarshal([]byte(`{"allow_insecure":`+value+`}`), &s)
		valid := value == "true" || value == "false"
		if (err == nil) != valid || valid && s.AllowInsecure != (value == "true") {
			t.Fatalf("wrong boolean decoding for %s: %v", value, err)
		}
	}
}

func TestDirectHTTPConsentSurvivesSaveReloadAndResourceReads(t *testing.T) {
	for _, mode := range []string{"beszel-password", "beszel-env", "komari-token", "komari-anonymous"} {
		t.Run(mode, func(t *testing.T) {
			var r Request
			var now time.Time
			if strings.HasPrefix(mode, "beszel") {
				h := fixtureHub(t)
				r = testRequest(h)
				now = h.at
				if mode == "beszel-env" {
					t.Setenv("TOPO_HTTP_TEST_TOKEN", h.token)
					r.Auth = Auth{Mode: "env", Name: "TOPO_HTTP_TEST_TOKEN"}
				}
			} else {
				r, now, _ = komariFixture(t)
				if mode == "komari-anonymous" {
					r.Auth = Auth{Mode: "anonymous"}
				}
			}
			// A DNS hostname deliberately exercises the non-literal-loopback
			// policy while keeping every actual request on the test server.
			r.Source.URL = strings.Replace(r.Source.URL, "127.0.0.1", "localhost", 1)
			st := testStore(t)
			if _, connection, err := st.Authenticate(context.Background(), r); err == nil {
				connection.Close()
				t.Fatal("discovery allowed HTTP without consent")
			}
			r.Source.AllowInsecure = true
			p := saveFixture(t, st, r)
			reopened, err := NewStore(st.Root)
			if err != nil {
				t.Fatal(err)
			}
			p, err = reopened.Load(p.Profile)
			if err != nil || !p.Sources[0].Source.AllowInsecure {
				t.Fatal("HTTP consent was not persisted", err)
			}
			// Reusing a saved credential must retain the source's policy.
			r.Auth = Auth{Mode: "saved"}
			r.BaseRevision = p.Revision
			p = saveFixture(t, reopened, r)
			session, err := NewSession(context.Background(), reopened, p, Options{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(session.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			session.Wait(ctx)
			snapshot, err := session.Snapshot("", now)
			if err != nil || snapshot.SourcesStatus.State != "ready" || len(snapshot.External) != 1 || snapshot.External[0].Report == nil {
				t.Fatal("HTTP resources unavailable after reload", err, snapshot.SourcesStatus)
			}
			// Runtime consent must not escape into collector topology JSON.
			c, err := p.Config()
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var wire model.Config
			if err := json.Unmarshal(data, &wire); err != nil || wire.Sources[0].AllowInsecure {
				t.Fatal("direct HTTP consent leaked into wire topology", err)
			}
			runtime := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
			if runtime.Validate(wire) == nil {
				t.Fatal("collector adapter accepted remote HTTP without direct consent")
			}
			p.Sources[0].Source.AllowInsecure = false
			if _, err := p.Config(); err == nil {
				t.Fatal("removing consent did not reject HTTP")
			}
		})
	}
}

func TestDirectHTTPConsentDoesNotDisableTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	for _, adapter := range []string{"beszel", "komari"} {
		t.Run(adapter, func(t *testing.T) {
			r := Request{Profile: "test", Source: SourceConfig{ID: "test", Name: "Test", Adapter: adapter, URL: server.URL, AllowInsecure: true, Interval: 30, Timeout: 8, Stale: 180, MaxAge: 180}, Auth: Auth{Mode: "token", Token: komariKey}}
			credential, connection, err := testStore(t).Authenticate(context.Background(), r)
			if err == nil {
				defer connection.Close()
				_, _, err = connection.Discover(context.Background(), credential)
			}
			if err == nil || AsError(err).Code != "tls_error" {
				t.Fatal("untrusted HTTPS accepted with HTTP consent", err)
			}
		})
	}
}

func TestTopologyJSONCannotEnableDirectHTTPConsent(t *testing.T) {
	var s model.Source
	if err := json.Unmarshal([]byte(`{"target":"http://100.86.138.20:8090","allow_insecure":true}`), &s); err != nil || s.AllowInsecure {
		t.Fatal("topology JSON enabled runtime HTTP consent", err)
	}
	// The generic configuration gate must agree with the adapter gate.
	h := fixtureHub(t)
	r := testRequest(h)
	r.Source.URL = "http://100.86.138.20:8090"
	r.Source.AllowInsecure = true
	p := Profile{Profile: "test", Project: "test", Sources: []Entry{{Source: r.Source, Selected: []string{testObject}, Nodes: []Node{{ID: r.Source.ID + "." + testObject, ObjectID: testObject, Name: "Test"}}}}}
	c, err := p.Config()
	if err != nil {
		t.Fatal(err)
	}
	c.Sources[0].AllowInsecure = false
	if config.Validate(c) == nil {
		t.Fatal("collector topology allowed credentialed HTTP")
	}
}
