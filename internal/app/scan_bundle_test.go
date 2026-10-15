package app

import (
	"bytes"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

// unsafe reports whether r is a rune a terminal may act on: a control,
// C0, DEL or C1, or a bidi control.
func unsafe(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) }

// JSON output escapes every control, a C1 CSI, OSC or ST as well as an
// ESC, and every bidi control, and reads back as what was written.
func TestJSONOutputEscapesAllControlsLosslessly(t *testing.T) {
	var value strings.Builder
	value.WriteString("before\x1b\nafter")
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unsafe(r) {
			value.WriteRune(r)
		}
	}
	var out bytes.Buffer
	original := map[string]string{value.String(): value.String()}
	if err := printJSON(&out, original); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.String() {
		if unsafe(r) && r != '\n' {
			t.Fatalf("literal control %U", r)
		}
	}
	var decoded map[string]string
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded[value.String()] != value.String() {
		t.Fatal("machine-readable content changed")
	}
	if !strings.Contains(out.String(), `\n`) {
		t.Fatal("newline not escaped")
	}
}

// What a collector says reaches stdout as JSON with no control in it: a
// C1 CSI, OSC or ST, which some terminals act on as on the escape
// sequence, no more than an ESC or a bidi control.
func TestRemoteJSONEscapesControls(t *testing.T) {
	c := testkit.Config()
	snapshot := engine.Build(c, "remote", "", nil, nil, time.Now().UTC())
	var controls strings.Builder
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unsafe(r) {
			controls.WriteRune(r)
		}
	}
	snapshot.Checks[0].Reason = controls.String() + "\u009b31m\u009d2;title\u009c\x1b\n"
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(snapshot)
	}))
	defer server.Close()
	out, _, err := invoke("", "--credentials", filepath.Join(t.TempDir(), "missing"), "--server", server.URL, "--project", c.Project, "--credential", token, "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		if unsafe(r) && r != '\n' {
			t.Fatalf("unsafe remote output %U", r)
		}
	}
	var decoded model.Snapshot
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Checks[0].Reason != snapshot.Checks[0].Reason {
		t.Fatal("remote diagnostics changed")
	}
}

// A collector that sends samples no worker could have, one taking 40 ms
// beside one taking less than nothing, has its history refused: show
// says so where the samples would be, rather than the process stopping.
func TestImpossibleDurationsAreRefusedNotDrawn(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	snapshot := engine.Build(c, "remote", "", nil, nil, now)
	contract := config.PublicContracts(c)["home-http"]
	entries := []model.Observation{testkit.Observation(c, "home-http", 1, now.Add(-2*time.Minute), "ok"), testkit.Observation(c, "home-http", 2, now.Add(-time.Minute), "ok")}
	entries[0].DurationMS, entries[1].DurationMS = 40, -40
	for i := range entries {
		entries[i].Contract = contract
	}
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/history" {
			json.NewEncoder(w).Encode(client.History{Project: c.Project, Revision: config.PublicRevision(c), Check: "home-http", Contract: contract, Entries: entries})
			return
		}
		json.NewEncoder(w).Encode(snapshot)
	}))
	defer server.Close()
	out, _, err := invoke("", "show", "checks", "home-http", "--credentials", filepath.Join(t.TempDir(), "missing"), "--server", server.URL, "--project", c.Project, "--credential", token)
	if err != nil || !strings.Contains(out, "unavailable: invalid collector history") {
		t.Fatalf("%v\n%s", err, out)
	}
}
