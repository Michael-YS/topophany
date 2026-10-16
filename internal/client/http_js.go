//go:build js && wasm

package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
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
	resp, err := t.inner.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	// Go's Fetch transport only watches cancellation until response headers
	// arrive. Closing its stream reader also releases a pending body Read.
	body := &browserBody{ReadCloser: resp.Body, ctx: r.Context()}
	body.stop = context.AfterFunc(r.Context(), func() { body.close() })
	resp.Body = body
	return resp, nil
}

func (t browserTransport) CloseIdleConnections() { t.inner.CloseIdleConnections() }

type browserBody struct {
	io.ReadCloser
	ctx  context.Context
	stop func() bool
	once sync.Once
	err  error
}

func (b *browserBody) close() { b.once.Do(func() { b.err = b.ReadCloser.Close() }) }

func (b *browserBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := b.ReadCloser.Read(p)
	if canceled := b.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

func (b *browserBody) Close() error {
	b.stop()
	b.close()
	return b.err
}
