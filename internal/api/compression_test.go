package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSnapshotCompressionNegotiationAndCacheBudget(t *testing.T) {
	s, handler, _ := fixture(t)
	for _, tc := range []struct {
		header string
		gzip   bool
	}{
		{"", false}, {"br", false}, {"gzip", true}, {"GZip; q=0.5", true}, {"gzip;q=0, *;q=1", false},
		{"*", true}, {"gzip;q=NaN", false}, {"gzip;q=2", false}, {"gzip;q=broken", false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/snapshot", nil)
			r.Header.Set("Authorization", "Bearer "+viewerToken)
			r.Header.Set("Accept-Encoding", tc.header)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusOK || (w.Header().Get("Content-Encoding") == "gzip") != tc.gzip {
				t.Fatal(w.Code, w.Header())
			}
			body := w.Body.Bytes()
			if tc.gzip {
				z, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, err = io.ReadAll(z)
				z.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, _, _ := s.cachedSnapshot(s.generation(), "", "principal")
			if !bytes.Equal(body, raw) {
				t.Fatal("compressed and identity snapshots differ")
			}
		})
	}
	held := 0
	for key, raw := range s.snapshotCache {
		held += len(raw) + len(s.snapshotGzip[key])
	}
	if held != s.snapshotCacheBytes || held > 32<<20 {
		t.Fatal("compressed cache not charged", held, s.snapshotCacheBytes)
	}
}

func TestSnapshotCacheRefreshesAfterClockRollback(t *testing.T) {
	s, handler, _ := fixture(t)
	clock := s.Now()
	s.Now = func() time.Time { return clock }
	first := request(handler, "GET", "/v1/snapshot", viewerToken, "", nil)
	clock = clock.Add(-time.Minute)
	second := request(handler, "GET", "/v1/snapshot", viewerToken, "", nil)
	var snap model.Snapshot
	if first.Code != http.StatusOK || second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &snap) != nil || !snap.GeneratedAt.Equal(clock) {
		t.Fatal("clock rollback reused a snapshot from the future", second.Code)
	}
}
