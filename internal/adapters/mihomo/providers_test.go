package mihomo

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProviderUpdatedAtIsOptionalMetadata(t *testing.T) {
	want := time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)
	if got := providerUpdatedAt(json.RawMessage(`"2026-10-17T06:00:00Z"`)); !got.Equal(want) {
		t.Fatalf("valid time: %v", got)
	}
	for _, raw := range []string{`""`, `0`, `"not a time"`, `null`, `{}`} {
		if got := providerUpdatedAt(json.RawMessage(raw)); !got.IsZero() {
			t.Fatalf("%s became %v; malformed metadata must not fail the check", raw, got)
		}
	}
}
