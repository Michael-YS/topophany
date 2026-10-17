package alert

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestWebhookResponseHeaderBudget(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run("http2="+strconv.FormatBool(http2), func(t *testing.T) {
			for _, size := range []int{16 << 10, 40 << 10, 2 << 20} {
				t.Run(strconv.Itoa(size), func(t *testing.T) {
					s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						io.Copy(io.Discard, r.Body)
						w.Header().Set("X-Large", strings.Repeat("a", size))
						w.WriteHeader(204)
					}))
					s.EnableHTTP2 = http2
					if http2 {
						s.StartTLS()
					} else {
						s.Start()
					}
					defer s.Close()
					n := &Notifier{Project: "lab", Instance: "test"}
					if http2 {
						transport := defaultClient.Transport.(*http.Transport).Clone()
						transport.TLSClientConfig = s.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
						c := *defaultClient
						c.Transport = transport
						n.Client = &c
						defer c.CloseIdleConnections()
					}
					status, failure := n.post(context.Background(), Webhook{ID: "local", Format: "json", URL: s.URL}, Event{Kind: "observer.silent"})
					if size < 32768 {
						if status != 204 || failure != "" {
							t.Fatal("normal headers refused", status, failure)
						}
					} else if status != 0 || failure == "" {
						t.Fatal("oversized response headers accepted", status, failure)
					}
				})
			}
		})
	}
}
