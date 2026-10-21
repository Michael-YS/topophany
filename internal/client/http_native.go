//go:build !js

package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"net"
	"net/http"
	"time"
)

func HTTP(ep Endpoint) (*http.Client, error) {
	if e := secure.URL(ep.URL, ep.AllowInsecure); e != nil {
		return nil, e
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if ep.CAFile != "" {
		b, e := secure.ReadBounded(ep.CAFile, 1<<20)
		if e != nil {
			return nil, errors.New("collector CA file unreadable")
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if len(b) > 1<<20 || !roots.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid collector CA file")
		}
		tc.RootCAs = roots
	}
	// Negotiate gzip in our wrapper so encoded and decoded reads both stay
	// bounded, including streams of empty gzip members.
	tr := &http.Transport{Proxy: nil, DisableCompression: true, TLSClientConfig: tc, MaxResponseHeaderBytes: 32768, TLSHandshakeTimeout: 4 * time.Second, ResponseHeaderTimeout: 6 * time.Second, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConnsPerHost: 2}
	return &http.Client{Transport: gzipTransport{tr}, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
