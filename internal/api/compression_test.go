package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
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

func TestSnapshotCompressionIsLazyAndReusesIdentityCapture(t *testing.T) {
	s, _, _ := fixture(t)
	g := s.generation()
	raw, status, code := s.cachedSnapshot(g, "", "viewer")
	if status != http.StatusOK || code != "" {
		t.Fatal(status, code)
	}
	if len(s.snapshotGzip) != 0 || s.snapshotCacheBytes != len(raw) {
		t.Fatal("identity request paid for unused compression")
	}
	builds := s.snapshotBuilds
	body, status, code := s.snapshotRepresentation(g, "", "viewer", true, "")
	if status != http.StatusOK || code != "" || body.encoding != "gzip" {
		t.Fatal(status, code, body.encoding)
	}
	if s.snapshotBuilds != builds || s.snapshotCacheBytes != len(raw)+len(body.data) {
		t.Fatal("lazy compression rebuilt state or bypassed accounting")
	}
	again, _, _ := s.snapshotRepresentation(g, "", "viewer", true, "")
	plain, _, _ := s.cachedSnapshot(g, "", "viewer")
	if &again.data[0] != &body.data[0] || !bytes.Equal(plain, raw) {
		t.Fatal("representations were rebuilt or identity changed")
	}
}

func TestLazyCompressionEvictsWithinBudgetAndRemembersNoSavings(t *testing.T) {
	s, _, _ := fixture(t)
	// Fill the byte budget, then request gzip for an existing identity entry.
	raw := bytes.Repeat([]byte("topophany"), 1024)
	s.snapshotCache = map[string][]byte{"wanted": raw, "other": make([]byte, (32<<20)-len(raw))}
	s.snapshotGzip = map[string][]byte{}
	s.snapshotCacheBytes = 32 << 20
	body := s.snapshotResponse("wanted", true)
	if body.encoding != "gzip" || !bytes.Equal(s.snapshotCache["wanted"], raw) {
		t.Fatal("requested representation lost during eviction")
	}
	if _, kept := s.snapshotCache["other"]; kept || s.snapshotCacheBytes != len(raw)+len(body.data) {
		t.Fatal("lazy gzip exceeded budget or evicted the wrong entry")
	}
	// An unhelpful result is cached too, so repeated requests need no work.
	noise := make([]byte, 2048)
	random := rand.New(rand.NewPCG(17, 91))
	for i := range noise {
		noise[i] = byte(random.Uint32())
	}
	for key, input := range map[string][]byte{"small": []byte("{}"), "incompressible": noise} {
		s.snapshotCache[key] = input
		s.snapshotCacheBytes += len(input)
		before := s.snapshotCacheBytes
		for range 3 {
			got := s.snapshotResponse(key, true)
			if zipped, tried := s.snapshotGzip[key]; !tried || zipped != nil || got.encoding != "" || !bytes.Equal(got.data, input) || s.snapshotCacheBytes != before {
				t.Fatal("unhelpful compression was not remembered", key)
			}
		}
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
