//go:build js && wasm

package client

import (
	"errors"
	"net/http"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// A browser supplies TLS trust and network access through Fetch. Custom
// DialContext functions disable Go's Fetch transport, so none may be set here.
func HTTP(ep Endpoint) (*http.Client, error) {
	if err := secure.URL(ep.URL, false); err != nil {
		return nil, err
	}
	if ep.CAFile != "" || ep.TokenEnv != "" || ep.AllowInsecure {
		return nil, errors.New("browser connections require browser-trusted TLS and an explicit viewer token")
	}
	return &http.Client{Transport: browserTransport{&http.Transport{}}, Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type browserTransport struct{ inner *http.Transport }

func (t browserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Del("User-Agent")
	r.Header.Set("js.fetch:mode", "cors")
	r.Header.Set("js.fetch:credentials", "omit")
	// Fetch follows redirects before net/http can check them; refuse them here
	// so a collector cannot forward the bearer token to a redirected endpoint.
	r.Header.Set("js.fetch:redirect", "error")
	return t.inner.RoundTrip(r)
}

func (t browserTransport) CloseIdleConnections() { t.inner.CloseIdleConnections() }
