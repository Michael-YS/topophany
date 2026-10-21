//go:build !js

package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestGzipSnapshotAndDecodedBudget(t *testing.T) {
	raw, err := json.Marshal(snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		raw     []byte
		corrupt bool
		valid   bool
	}{
		{"valid", raw, false, true},
		{"decoded-overflow", []byte(strings.Repeat(" ", model.MaxSnapshotBytes+1)), false, false},
		{"checksum", raw, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			z := gzip.NewWriter(&buf)
			if _, err := z.Write(tc.raw); err != nil {
				t.Fatal(err)
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			compressed := bytes.Clone(buf.Bytes())
			if tc.corrupt {
				compressed[len(compressed)-8] ^= 1
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "gzip" {
					t.Error("gzip not negotiated")
				}
				w.Header().Set("Content-Encoding", "gzip")
				w.Write(compressed)
			}))
			defer srv.Close()
			c := newClient(t, srv.URL)
			_, _, err := c.Snapshot(context.Background(), "")
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
