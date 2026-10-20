package app

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/preferences"
)

func TestPreferenceCLIOverridesAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	_, _, err := invoke("", "preferences", "set", "--preferences", path, "--page", "servers", "--uniform-cards", "--service-cards", "--groups-collapsed", "--full-map", "--map-mode", "geo", "--nerd-font-icons")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	p := addPreferenceFlags(f)
	if err = f.Parse([]string{"--preferences", path, "--page", "overview", "--uniform-cards=false", "--full-map=false"}); err != nil {
		t.Fatal(err)
	}
	got, err := p.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.Page != "overview" || got.Settings.UniformCards || !got.Settings.ServiceCards || got.Settings.FullMap || !got.Settings.GroupsCollapsed || got.Settings.MapMode != "geo" || !got.Settings.NerdFontIcons {
		t.Fatal(got)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("temporary overrides wrote defaults")
	}
	_, _, err = invoke("", "preferences", "set", "--preferences", path, "--uniform-cards=false", "--service-cards=false")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := preferences.Load(path)
	if err != nil || saved.Page != "servers" || saved.UniformCards || saved.ServiceCards {
		t.Fatal(saved, err)
	}
	_, _, err = invoke("", "preferences", "set", "--preferences", path, "--page", "invalid")
	if err == nil {
		t.Fatal("accepted invalid page")
	}
	_, _, err = invoke("", "preferences", "reset", "--preferences", path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err = preferences.Load(path)
	if err != nil || saved != preferences.Defaults() {
		t.Fatal(saved, err)
	}
}

func TestPreferencesResetRepairsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"page":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke("", "preferences", "show", "--preferences", path); err == nil {
		t.Fatal("corrupt defaults ignored")
	}
	if _, _, err := invoke("", "preferences", "reset", "--preferences", path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke("", "preferences", "show", "--preferences", path); err != nil {
		t.Fatal(err)
	}
}

// Saved preferences that cannot be read do not keep the TUI from opening:
// the defaults stand in and the TUI is told why; flags must still be valid.
func TestUnreadablePreferencesOpenWithDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"page":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	f := flag.NewFlagSet("topo", flag.ContinueOnError)
	p := addPreferenceFlags(f)
	if err := f.Parse([]string{"--preferences", path}); err != nil {
		t.Fatal(err)
	}
	s, err := p.startup()
	if err != nil || s.Settings != preferences.Defaults() || !strings.Contains(s.Notice, "topo preferences reset") {
		t.Fatal(s, err)
	}
	f = flag.NewFlagSet("topo", flag.ContinueOnError)
	p = addPreferenceFlags(f)
	if err := f.Parse([]string{"--preferences", path, "--page", "bogus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.startup(); err == nil {
		t.Fatal("an invalid --page accepted")
	}
}
