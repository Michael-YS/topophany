package app

import (
	"bytes"
	"encoding/json"
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

func TestJSONOutputEscapesAllBidiControlsLosslessly(t *testing.T) {
	var value strings.Builder
	value.WriteString("before\x1b\nafter")
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.Is(unicode.Bidi_Control, r) {
			value.WriteRune(r)
		}
	}
	var out bytes.Buffer
	original := map[string]string{value.String(): value.String()}
	if err := printJSON(&out, original); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.String() {
		if unicode.Is(unicode.Bidi_Control, r) || r == 0x1b {
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

func TestRemoteJSONEscapesBidiControls(t *testing.T) {
	c := testkit.Config()
	snapshot := engine.Build(c, "remote", "", nil, nil, time.Now().UTC())
	var controls strings.Builder
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.Is(unicode.Bidi_Control, r) {
			controls.WriteRune(r)
		}
	}
	snapshot.Checks[0].Reason = controls.String() + "\x1b\n"
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
		if unicode.Is(unicode.Bidi_Control, r) || r == 0x1b {
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
