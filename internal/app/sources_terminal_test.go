package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcesEscapesTerminalControls(t *testing.T) {
	for _, id := range []string{"\u009b", "\u202e", "\u007f", "中文"} {
		t.Run(id, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"schema": 1, "request_id": id, "profile": "desktop", "source_id": "audit"})
			var out bytes.Buffer
			a := &App{In: bytes.NewReader(raw), Out: &out, Err: io.Discard}
			_ = a.Run(context.Background(), []string{"sources", "remove", "--json-stdin", "--direct-root", filepath.Join(t.TempDir(), "direct")})
			var decoded struct {
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.RequestID != id {
				t.Fatalf("request id not roundtripped: %q", decoded.RequestID)
			}
			if id != "中文" && strings.Contains(out.String(), id) {
				t.Errorf("raw terminal control in JSON: %q", out.String())
			}
		})
	}
}
