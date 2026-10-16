package tui

import (
	"github.com/WhitenWhiten/topophany/internal/preferences"
	"slices"
)

// Startup carries the resolved defaults and the location to save explicitly.
type Startup struct {
	Settings  preferences.Settings
	Path      string
	Overrides []string
}

func (m *Model) initializeStartup(options []Startup) {
	s := Startup{Settings: preferences.Defaults()}
	if len(options) > 0 {
		s = options[0]
	}
	m.PreferencesPath = s.Path
	m.PreferenceOverrides = s.Overrides
	m.applyPreferences(s.Settings)
	for i, field := range preferenceForm(s.Settings).fields {
		m.preferenceInitial[i] = field.value
	}
}
func (m *Model) applyPreferences(s preferences.Settings) {
	m.setTab(slices.Index(preferences.Pages, s.Page))
	m.GroupsCollapsed = s.GroupsCollapsed
	m.Expanded = nil
	m.Uniform = s.UniformCards
	m.FullMap = s.FullMap
	m.MapMode = s.MapMode
	m.NerdFont = s.NerdFontIcons
	if m.MapMode == "auto" {
		m.MapMode = ""
	}
}
func (m *Model) currentPreferences() preferences.Settings {
	mode := m.MapMode
	if mode == "" {
		mode = "auto"
	}
	return preferences.Settings{Page: preferences.Pages[m.Tab], GroupsCollapsed: m.GroupsCollapsed, UniformCards: m.Uniform, FullMap: m.FullMap, MapMode: mode, NerdFontIcons: m.NerdFont}
}
