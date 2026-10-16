package preferences

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "preferences.json")
	s, err := Load(path)
	if err != nil || s != Defaults() {
		t.Fatal(s, err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read created a preferences file")
	}
	s.Page = "servers"
	s.UniformCards = true
	s.GroupsCollapsed = true
	s.FullMap = true
	s.MapMode = "network"
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != s {
		t.Fatal(got, err)
	}
	for _, raw := range []string{`{"page":"bogus"}`, `{"map_mode":"bogus"}`, `{"unknown":true}`, `{"uniform_cards":"yes"}`, `{} {}`} {
		if err = os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(path); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	if err = Save(path, Defaults()); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"page":"checks"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil || got.Page != "checks" || got.MapMode != "auto" {
		t.Fatal(got, err)
	}
}
