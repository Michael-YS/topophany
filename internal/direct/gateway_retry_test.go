package direct

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDirectGatewayFailuresRecoverWithoutManualRetry(t *testing.T) {
	for _, status := range []int{408, 500, 501, 503, 507, 520, 522, 524, 599} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := fixtureHub(t)
			st := testStore(t)
			p := saveFixture(t, st, testRequest(h))
			h.mu.Lock()
			h.statsStatus = status
			h.at = time.Now().UTC()
			h.mu.Unlock()
			s, e := NewSession(context.Background(), st, p, Options{Backoff: func(int) time.Duration { return 20 * time.Millisecond }})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s.Wait(ctx)
			snap, e := s.Snapshot("", time.Now())
			if e != nil {
				t.Fatal(e)
			}
			src := snap.Direct.Sources[0]
			if src.State != "retrying" || src.Code == nil || *src.Code != "connection_failed" {
				t.Fatalf("HTTP %d not retrying: %+v", status, src)
			}
			h.mu.Lock()
			h.statsStatus = 0
			h.mu.Unlock()
			for {
				snap, e = s.Snapshot("", time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if snap.Direct.Sources[0].State == "connected" {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("did not recover", snap.Direct.Sources[0])
				case <-time.After(5 * time.Millisecond):
				}
			}
		})
	}
}

func TestFailureRetryFlagMatchesSessionPolicy(t *testing.T) {
	for _, code := range []string{"connection_failed", "tls_error", "rate_limited", "credential_renewed", "auth_required", "forbidden", "upstream_incompatible"} {
		state, retry := failurePolicy(code)
		if fail(code).Retryable != retry || retry != (state == "retrying") {
			t.Fatal(code, state, retry)
		}
	}
}
