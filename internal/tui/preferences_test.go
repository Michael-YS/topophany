package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/preferences"
)

func TestStartupAndDefaultExpandedGroups(t *testing.T) {
	m := businessFixture()
	m.GroupsCollapsed = false
	if len(m.Rows()) <= 1 {
		t.Fatal("groups must start expanded")
	}
	m.Key("enter")
	if len(m.Rows()) != 1 {
		t.Fatal("cannot collapse default expanded group")
	}
	m.Key("enter")
	if len(m.Rows()) <= 1 {
		t.Fatal("cannot reopen group")
	}
	s := preferences.Defaults()
	s.Page = "servers"
	s.UniformCards = true
	s.FullMap = true
	s.MapMode = "network"
	s.GroupsCollapsed = true
	m.initializeStartup([]Startup{{Settings: s}})
	if m.Tab != tabServers || !m.Uniform || !m.FullMap || m.MapMode != "network" {
		t.Fatal("startup options not applied")
	}
	m.setTab(tabServices)
	if len(m.Rows()) != 1 {
		t.Fatal("collapse preference ignored")
	}
	m.setTab(tabTopology)
	if !m.FullMap {
		t.Fatal("full map preference lost across tabs")
	}
}
func TestPreferencesApplySaveResetAndCancel(t *testing.T) {
	m := fixtureModel()
	path := filepath.Join(t.TempDir(), "preferences.json")
	m.initializeStartup([]Startup{{Settings: preferences.Defaults(), Path: path}})
	m.Key(",")
	if m.modal == nil || m.modal.kind != "preferences" {
		t.Fatal("settings shortcut")
	}
	m.modal.form.get("Startup page").value = "servers"
	m.modal.form.get("Uniform cards").value = "yes"
	m.Key("pref:apply")
	if m.Tab != tabServers || !m.Uniform {
		t.Fatal("apply failed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("apply persisted defaults")
	}
	m.Key(",")
	m.modal.form.get("Startup page").value = "checks"
	m.Key("pref:save")
	if m.Tab != tabServers {
		t.Fatal("saving changed session")
	}
	s, err := preferences.Load(path)
	if err != nil || s.Page != "checks" || !s.UniformCards {
		t.Fatal(s, err)
	}
	m.Key(",")
	m.Key("pref:reset")
	if formPreferences(m.modal.form) != preferences.Defaults() || m.Tab != tabServers {
		t.Fatal("reset mutated session")
	}
	m.Key("esc")
	s, _ = preferences.Load(path)
	if s.Page != "checks" {
		t.Fatal("reset persisted before save")
	}
	m.Key(",")
	m.Key("pref:reset")
	m.Key("pref:save")
	s, _ = preferences.Load(path)
	if s != preferences.Defaults() {
		t.Fatal(s)
	}
	m.Searching = true
	m.Key(",")
	if m.modal != nil || m.Search != "," {
		t.Fatal("comma intercepted search input")
	}
}
func TestPreferencesRenderAndSaveFailure(t *testing.T) {
	m := fixtureModel()
	s := preferences.Defaults()
	s.Page = "servers"
	m.initializeStartup([]Startup{{Settings: s, Path: t.TempDir(), Overrides: []string{"page"}}})
	m.Key(",")
	if !strings.Contains(strings.Join(m.Render(), "\n"), "CLI override") {
		t.Fatal("CLI origin missing")
	}
	m.Key("pref:save")
	if m.modal == nil || m.modal.form.err == "" {
		t.Fatal("save failure dismissed form")
	}
	for _, size := range [][2]int{{40, 16}, {80, 24}, {160, 48}} {
		m.Width, m.Height = size[0], size[1]
		for i := 0; i < 8; i++ {
			m.modal.form.focus = i
			out := m.Render()
			for _, line := range out {
				if width(ansi.ReplaceAllString(line, "")) > m.Width {
					t.Fatal("settings overflow")
				}
			}
		}
	}
	m.Key("esc")
	m.setTab(tabAbout)
	m.Render()
	found := false
	for _, h := range m.hits {
		if h.key == "key:," {
			found = true
		}
	}
	if !found {
		t.Fatal("About settings entry missing")
	}
}

func TestPreferencesCommaDoesNotInterceptEditorText(t *testing.T) {
	m, _ := editorModel(t)
	m.openForm("service", m.Edit.Config.Services[0].ID)
	f := m.modal.form
	for i, field := range f.fields {
		if field.label == "Name" {
			f.focus = i
			field.value = "name"
			field.cursor = 4
		}
	}
	m.Key(",")
	if m.modal.kind != "form" || f.get("Name").value != "name," {
		t.Fatal("comma intercepted editor field")
	}
}
