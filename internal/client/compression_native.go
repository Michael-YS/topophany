//go:build !js

package client

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

type gzipTransport struct{ inner *http.Transport }

func (t gzipTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Accept-Encoding", "gzip")
	resp, err := t.inner.RoundTrip(r)
	if err != nil || !strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		return resp, err
	}
	z, err := gzip.NewReader(io.LimitReader(resp.Body, model.MaxSnapshotBytes+1))
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	resp.Body = &gzipBody{Reader: z, original: resp.Body}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
	return resp, nil
}

func (t gzipTransport) CloseIdleConnections() { t.inner.CloseIdleConnections() }

type gzipBody struct {
	*gzip.Reader
	original io.ReadCloser
}

func (b *gzipBody) Close() error {
	_ = b.Reader.Close()
	return b.original.Close()
}
