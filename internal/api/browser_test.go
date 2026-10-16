package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserOriginMustBeExplicitAndExact(t *testing.T) {
	for _, origin := range []string{"https://topophany.com", "https://monitor.example:8443", "http://127.0.0.1:8000", ""} {
		if err := ValidateWebOrigin(origin); err != nil {
			t.Fatal(origin, err)
		}
	}
	for _, origin := range []string{"*", "null", "https://*.example.com", "http://example.com", "https://topophany.com/", "https://topophany.com/app/", "https://a:b@example.com", "https://example.com?token=secret"} {
		if ValidateWebOrigin(origin) == nil {
			t.Fatal("accepted unsafe origin", origin)
		}
	}
	s, _, _ := fixture(t)
	r := httptest.NewRequest("OPTIONS", "/v1/snapshot", nil)
	r.Header.Set("Origin", "https://topophany.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Headers", "authorization")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("browser access enabled by default")
	}
}

func TestBrowserPreflightNeverGrantsDataOrWorkerWrites(t *testing.T) {
	s, _, _ := fixture(t)
	s.WebOrigin = "https://topophany.com"
	h := s.Handler()
	for _, tc := range []struct {
		method, path, origin, token, requested, headers string
		status                                          int
	}{
		{"OPTIONS", "/v1/snapshot", s.WebOrigin, "", "GET", "authorization", 204},
		{"OPTIONS", "/v1/history", s.WebOrigin, "", "GET", "Authorization", 204},
		{"OPTIONS", "/v1/rollups", s.WebOrigin, "", "POST", "authorization", 403},
		{"OPTIONS", "/v1/config", s.WebOrigin, "", "GET", "authorization,x-custom", 403},
		{"OPTIONS", "/v1/snapshot", "https://topophany.com.evil.invalid", "", "GET", "authorization", 403},
		{"OPTIONS", "/v1/observations", s.WebOrigin, "", "POST", "authorization", 403},
		{"GET", "/v1/snapshot", s.WebOrigin, "", "", "", 401},
		{"GET", "/v1/snapshot", s.WebOrigin, workerToken, "", "", 403},
		{"GET", "/v1/snapshot", s.WebOrigin, viewerToken, "", "", 200},
		{"GET", "/v1/snapshot", "https://evil.invalid", viewerToken, "", "", 403},
		{"GET", "/v1/snapshot", "", viewerToken, "", "", 200},
		{"POST", "/v1/observations", s.WebOrigin, workerToken, "", "", 403},
	} {
		t.Run(tc.method+tc.path+tc.origin+tc.token[:min(1, len(tc.token))]+tc.requested+tc.headers, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Access-Control-Request-Method", tc.requested)
			r.Header.Set("Access-Control-Request-Headers", tc.headers)
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			allow := w.Header().Get("Access-Control-Allow-Origin")
			if tc.origin == s.WebOrigin && browserRead(tc.path) {
				if allow != s.WebOrigin {
					t.Fatal("missing exact origin", allow)
				}
			} else if allow != "" {
				t.Fatal("reflected untrusted origin", allow)
			}
			if w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("browser cookies enabled")
			}
			if tc.method == http.MethodOptions && tc.status == 204 && w.Body.Len() != 0 {
				t.Fatal("preflight exposed data")
			}
			if strings.Contains(w.Body.String(), viewerToken) || strings.Contains(w.Body.String(), workerToken) {
				t.Fatal("token reflected")
			}
		})
	}
}
