package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// ValidateWebOrigin accepts one explicit browser origin, or empty to disable
// browser reads. Origins contain no credentials, path, query or wildcard.
func ValidateWebOrigin(origin string) error {
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || len(origin) > 512 || u.Path != "" || strings.Contains(origin, "*") || secure.URL(origin, false) != nil {
		return errors.New("web-origin must be an exact HTTPS origin, or literal HTTP loopback, without a path")
	}
	return nil
}

func browserRead(path string) bool {
	switch path {
	case "/v1/snapshot", "/v1/config", "/v1/history", "/v1/rollups":
		return true
	}
	return false
}

// browser runs inside the existing authentication/admission budgets. CORS
// only permits reading viewer routes; each actual GET still needs its token.
func (s *Server) browser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if s.WebOrigin == "" || origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		if origin != s.WebOrigin || !browserRead(r.URL.Path) {
			writeError(w, http.StatusForbidden, "browser_origin_or_route_rejected")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", s.WebOrigin)
		if r.Method == http.MethodOptions {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			if r.Header.Get("Access-Control-Request-Method") != http.MethodGet {
				writeError(w, http.StatusForbidden, "browser_method_rejected")
				return
			}
			for _, name := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
				if name = strings.TrimSpace(name); name != "" && !strings.EqualFold(name, "Authorization") {
					writeError(w, http.StatusForbidden, "browser_header_rejected")
					return
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization")
			w.Header().Set("Access-Control-Max-Age", "300")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
