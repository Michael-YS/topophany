package direct

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A renewable token refreshes once its one-minute window opens. A
// nonrenewable token has no refresh, so it stays usable until it expires and
// only then requires authentication again.
func TestCredentialExpiryWindows(t *testing.T) {
	h := fixtureHub(t)
	st := testStore(t)
	p := saveFixture(t, st, testRequest(h))
	entry := p.Sources[0]
	path := filepath.Join(st.Root, "credential-"+entry.Credential+".json")
	now := time.Now().Truncate(time.Second)
	save := func(remaining time.Duration, renewable bool) string {
		t.Helper()
		c, e := st.credential(entry.Credential)
		if e != nil {
			t.Fatal(e)
		}
		c.Token = jwt(now.Add(remaining), renewable)
		c.Renewable = renewable
		b, _ := json.Marshal(c)
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		return c.Token
	}
	refreshes := func() int {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.counts["POST /api/collections/users/auth-refresh"]
	}
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		renewable bool
		usable    bool
		refresh   bool
	}{
		{"nonrenewable before window", 61 * time.Second, false, true, false},
		{"nonrenewable at window", 60 * time.Second, false, true, false},
		{"nonrenewable one second left", time.Second, false, true, false},
		{"nonrenewable at expiry", 0, false, false, false},
		{"nonrenewable past expiry", -time.Second, false, false, false},
		{"renewable before window", 61 * time.Second, true, true, false},
		{"renewable at window", 60 * time.Second, true, true, true},
		{"renewable one second left", time.Second, true, true, true},
		{"renewable at expiry", 0, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := save(tc.remaining, tc.renewable)
			before := refreshes()
			token, e := st.Token(context.Background(), p.Profile, entry, now)
			if tc.usable != (e == nil) {
				t.Fatalf("usable=%v, error=%v", tc.usable, e)
			}
			if !tc.usable {
				if AsError(e).Code != "auth_required" {
					t.Fatalf("error code %q", AsError(e).Code)
				}
			} else if tc.refresh && token != h.token {
				t.Fatal("renewable token was not renewed")
			} else if !tc.refresh && token != saved {
				t.Fatal("token that needed no renewal was not the saved one")
			}
			want := 0
			if tc.refresh {
				want = 1
			}
			if got := refreshes() - before; got != want {
				t.Fatalf("refresh requests %d, want %d", got, want)
			}
		})
	}
}
