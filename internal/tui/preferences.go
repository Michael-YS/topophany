package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/preferences"
)

var preferenceNames = []string{"page", "groups-collapsed", "uniform-cards", "full-map", "map-mode", "nerd-font-icons"}

// prefFields is how many preferences the dialog lists; its three buttons
// follow them.
const prefFields = 6

func preferenceForm(s preferences.Settings) *Form {
	f := &Form{}
	f.add("Startup page", s.Page, fChoice, "", preferences.Pages...)
	groups := "expanded"
	if s.GroupsCollapsed {
		groups = "collapsed"
	}
	f.add("Initial groups", groups, fChoice, "", "expanded", "collapsed")
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	f.add("Uniform cards", yes(s.UniformCards), fBool, "")
	f.add("Full map", yes(s.FullMap), fBool, "")
	f.add("Map mode", s.MapMode, fChoice, "", preferences.MapModes...)
	f.add("Nerd Font icons", yes(s.NerdFontIcons), fBool, "")
	return f
}
func formPreferences(f *Form) preferences.Settings {
	return preferences.Settings{Page: f.val("Startup page"), GroupsCollapsed: f.val("Initial groups") == "collapsed", UniformCards: f.yes("Uniform cards"), FullMap: f.yes("Full map"), MapMode: f.val("Map mode"), NerdFontIcons: f.yes("Nerd Font icons")}
}
func (m *Model) openPreferences() {
	f := preferenceForm(m.currentPreferences())
	m.modal = &modal{kind: "preferences", title: "Display & startup preferences", form: f}
	for i, field := range f.fields {
		if slices.Contains(m.PreferenceOverrides, preferenceNames[i]) && field.value == m.preferenceInitial[i] {
			m.modal.lines = append(m.modal.lines, preferenceNames[i])
		}
	}
}
func (m *Model) preferencesKey(k string) string {
	f := m.modal.form
	switch k {
	case "esc":
		m.modal = nil
		return ""
	case "ctrl-c":
		return "quit"
	case "tab", "down", "wheel-down":
		f.focus = (f.focus + 1) % (prefFields + 3)
		return ""
	case "shift-tab", "up", "wheel-up":
		f.focus = (f.focus + prefFields + 2) % (prefFields + 3)
		return ""
	case "enter":
		if f.focus < prefFields {
			f.focus++
			return ""
		}
		k = []string{"pref:apply", "pref:save", "pref:reset"}[f.focus-prefFields]
	}
	if strings.HasPrefix(k, "pref:field:") {
		i, err := strconv.Atoi(strings.TrimPrefix(k, "pref:field:"))
		if err == nil && i >= 0 && i < prefFields {
			f.focus = i
			f.key("right")
			m.modal.lines = slices.DeleteFunc(m.modal.lines, func(name string) bool { return name == preferenceNames[i] })
		}
		return ""
	}
	switch k {
	case "pref:reset":
		m.modal.lines = nil
		m.modal.form = preferenceForm(preferences.Defaults())
		m.modal.form.note = "Built-in defaults in form; apply or save to confirm."
		return ""
	case "pref:apply", "pref:save":
		s := formPreferences(f)
		if err := s.Validate(); err != nil {
			f.err = err.Error()
			return ""
		}
		if k == "pref:save" {
			path := m.PreferencesPath
			if path == "" {
				var err error
				path, err = preferences.DefaultPath()
				if err != nil {
					f.err = err.Error()
					return ""
				}
			}
			if err := preferences.Save(path, s); err != nil {
				f.err = err.Error()
				return ""
			}
			m.PreferencesPath = path
			// Saving changes future launches only; applying is a separate action.
			m.modal = nil
			m.say("Startup defaults saved; current session unchanged", false)
		} else {
			m.applyPreferences(s)
			m.PreferenceOverrides = nil
			m.modal = nil
			m.say("Display preferences applied to this session only", false)
		}
		return ""
	}
	if f.focus < prefFields {
		before := f.fields[f.focus].value
		f.key(k)
		if f.fields[f.focus].value != before {
			f.note = ""
			m.modal.lines = slices.DeleteFunc(m.modal.lines, func(name string) bool { return name == preferenceNames[f.focus] })
		}
	}
	return ""
}
func (m *Model) drawPreferences(cv *Canvas) {
	f := m.modal.form
	x, y, w, h := m.modalBox(cv, 76, 17, m.modal.title)
	bg := Style{BG: cPanelBG}
	message := "Apply: this session · Save: future launches"
	if len(m.modal.lines) > 0 {
		message = "* CLI override for this launch"
	}
	if f.note != "" {
		message = f.note
	}
	cv.Text(x, y, ellipsis(message, w), bg.Fg(cMuted), w)
	// Keep the focused item visible even on a short terminal.
	available := max(1, h-4)
	start := max(0, f.focus-available+1)
	for i := start; i < min(prefFields+3, start+available); i++ {
		yy := y + 2 + i - start
		style := bg.Fg(cText)
		if i == f.focus {
			style = bg.Fg(cGlow).bold()
			cv.Fill(x, yy, w, 1, Style{BG: cSelBG})
			style = style.Bg(cSelBG)
			cv.Set(x-1, yy, '›', style)
		}
		if i < prefFields {
			label := f.fields[i].label
			if slices.Contains(m.modal.lines, preferenceNames[i]) {
				label += " *"
			}
			labelW := min(24, w/2)
			cv.Text(x, yy, ellipsis(label, labelW), style, labelW)
			cv.Text(x+labelW+1, yy, ellipsis("‹ "+f.fields[i].value+" ›", w-labelW-1), style, w-labelW-1)
			m.hit(x, yy, w, "pref:field:"+strconv.Itoa(i))
		} else {
			label := []string{"Apply to this session", "Save as startup defaults", "Restore built-in defaults"}[i-prefFields]
			cv.Text(x, yy, ellipsis("[ "+label+" ]", w), style, w)
			m.hit(x, yy, w, []string{"pref:apply", "pref:save", "pref:reset"}[i-prefFields])
		}
	}
	if f.err != "" {
		cv.Text(x, y+h-1, ellipsis(f.err, w), bg.Fg(cDown), w)
	} else {
		cv.Text(x, y+h-1, "Tab move · ←→ change · Enter select · Esc cancel", bg.Fg(cMuted), w)
	}
}
