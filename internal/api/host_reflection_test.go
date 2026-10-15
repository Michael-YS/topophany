package api

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// Private request headers stay private through a verified HTTPS probe,
// worker ingestion and both viewer evidence endpoints, including Host
// overrides stored outside Request.Header. Public edge labels remain useful.
func TestPrivateRequestHeadersCannotReflectIntoViewerEvidence(t *testing.T) {
	for _, header := range []string{"Host", "host", "hOsT", "X-API-Key", ""} {
		name := header
		if name == "" {
			name = "public request"
		}
		t.Run(name, func(t *testing.T) {
			const private = "private-vhost.invalid"
			const publicLabel = "public-vhost.invalid"
			peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				value := publicLabel
				if header != "" {
					value = r.Header.Get(header)
					if strings.EqualFold(header, "Host") {
						value = r.Host
					}
					if value != private {
						t.Errorf("configured header was not sent: got %q", value)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
				}
				w.Header().Set("X-Vercel-Id", value+"::"+value+"::x")
				io.WriteString(w, "ok")
			}))
			cert, key, ca := testkit.Certificate(t)
			pair, err := tls.LoadX509KeyPair(cert, key)
			if err != nil {
				t.Fatal(err)
			}
			peer.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
			peer.StartTLS()
			defer peer.Close()

			srv, h, b := fixture(t)
			c := config.Clone(srv.Config)
			x := &c.Checks[0]
			x.Target, x.CAFile = peer.URL, ca
			if header != "" {
				x.HTTP.RequestHeaders = map[string]string{header: private}
			}
			if err := config.Validate(c); err != nil {
				t.Fatal(err)
			}
			srv.Reload(c, srv.Auth)
			public := request(h, "GET", "/v1/config", viewerToken, "", nil)
			if public.Code != 200 || strings.Contains(public.Body.String(), private) {
				t.Fatal("private header was not redacted from topology")
			}
			v := (probe.Runner{}).Run(context.Background(), c, *x)
			if v.Outcome != "ok" || v.Facts["response_proof"] != "verified_https" || v.Facts["edge_provider"] != "vercel" {
				t.Fatalf("verified probe evidence lost: %+v", v)
			}
			if header == "" && (v.Facts["edge_pop"] != publicLabel || v.Facts["edge_region"] != publicLabel) {
				t.Fatal("public edge labels lost", v.Facts)
			}
			identity := testkit.Observation(c, x.ID, 1, v.SampledAt, "ok")
			v.ID, v.Boot, v.Sequence = identity.ID, identity.Boot, identity.Sequence
			b.Revision, b.Observations = config.Revision(c), []model.Observation{v}
			stored := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
			if stored.Code != 202 {
				t.Fatalf("ingestion failed: %d %s", stored.Code, stored.Body.String())
			}
			for _, path := range []string{"/v1/history?check=home-http", "/v1/snapshot"} {
				response := request(h, "GET", path, viewerToken, "", nil)
				if response.Code != 200 {
					t.Fatalf("%s: status=%d body=%s", path, response.Code, response.Body.String())
				}
				if strings.Contains(response.Body.String(), private) {
					t.Fatalf("%s exposes a private request header", path)
				}
				if header == "" && !strings.Contains(response.Body.String(), publicLabel) {
					t.Fatalf("%s lost public edge labels", path)
				}
			}
		})
	}
}
