package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestEditorCloudflaredKindKeepsNestedOptions(t *testing.T) {
	if !slices.Contains(checkKinds, "cloudflared") {
		t.Fatal("cloudflared is not offered by the check form")
	}
	m, _ := editorModel(t)
	c := m.Edit.Config
	x := &c.Checks[0]
	x.Kind, x.Target, x.Endpoint, x.Path, x.Service = "cloudflared", "http://127.0.0.1:20241", "", "", ""
	x.HTTP = model.HTTPOptions{}
	four := 4
	x.Adapter.Cloudflared = &model.CloudflaredOptions{ExpectedConnections: &four, ExpectedColos: []string{"HKG", "NRT"}}
	m.Edit.Config = c
	m.rebuild()
	m.openForm("check", x.ID)
	f := m.modal.form
	if f.val("Kind") != "cloudflared" || !slices.Contains(f.get("Kind").choices, "cloudflared") {
		t.Fatalf("form did not start on cloudflared: %q", f.val("Kind"))
	}
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatalf("form did not save: %v", m.modal.form.err)
	}
	if err := config.Validate(m.Edit.Config); err != nil {
		t.Fatal(err)
	}
	got := m.Edit.Config.Checks[0].Adapter.Cloudflared
	if got == nil || got.ExpectedConnections == nil || *got.ExpectedConnections != 4 || !slices.Equal(got.ExpectedColos, []string{"HKG", "NRT"}) {
		t.Fatalf("unlisted nested options lost: %+v", got)
	}
}

// The check form authors a connector check: its target and, shown only for
// that kind, the sessions and colos the deployment declares.
func TestEditorCloudflaredCreateFromForm(t *testing.T) {
	m, _ := editorModel(t)
	m.openForm("check", "")
	f := m.modal.form
	shown := func(label string) bool {
		for i, fd := range f.fields {
			if fd.label == label {
				return f.shown(i)
			}
		}
		t.Fatalf("no field %q", label)
		return false
	}
	for _, label := range []string{"Cloudflared expected connections", "Cloudflared expected colos"} {
		if shown(label) {
			t.Fatalf("%q is on the form of an http check", label)
		}
	}
	for label, value := range map[string]string{
		"ID":                               "home-tunnel",
		"Kind":                             "cloudflared",
		"Target":                           "http://127.0.0.1:20241",
		"Cloudflared expected connections": "4",
		"Cloudflared expected colos":       "hkg, sin",
	} {
		f.get(label).value = value
	}
	for _, label := range []string{"Cloudflared expected connections", "Cloudflared expected colos"} {
		if !shown(label) {
			t.Fatalf("%q is not on the form of a cloudflared check", label)
		}
	}
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatalf("cloudflared form rejected: %v", m.modal.form.err)
	}
	x, ok := config.Check(m.Edit.Config, "home-tunnel")
	if !ok || x.Kind != "cloudflared" || x.Target != "http://127.0.0.1:20241" || x.Adapter.Cloudflared == nil {
		t.Fatalf("check not created: %+v", x)
	}
	cf := x.Adapter.Cloudflared
	if cf.ExpectedConnections == nil || *cf.ExpectedConnections != 4 || !slices.Equal(cf.ExpectedColos, []string{"hkg", "sin"}) {
		t.Fatalf("options %+v", cf)
	}
	if err := config.Validate(m.Edit.Config); err != nil {
		t.Fatal(err)
	}

	// Editing it again shows what it declares; clearing both leaves no empty
	// options behind.
	m.openForm("check", "home-tunnel")
	f = m.modal.form
	if f.val("Cloudflared expected connections") != "4" || f.val("Cloudflared expected colos") != "hkg, sin" {
		t.Fatalf("the form lost the declaration: %q, %q", f.val("Cloudflared expected connections"), f.val("Cloudflared expected colos"))
	}
	f.get("Cloudflared expected connections").value, f.get("Cloudflared expected colos").value = "", ""
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatalf("form did not save: %v", m.modal.form.err)
	}
	if x, _ := config.Check(m.Edit.Config, "home-tunnel"); x.Adapter.Cloudflared != nil {
		t.Fatalf("an empty declaration was kept: %+v", x.Adapter.Cloudflared)
	}
}

// What the declaration may not be is said by the validation the form already
// runs, and the draft stays as it was.
func TestEditorCloudflaredRefusesWhatValidationRefuses(t *testing.T) {
	for _, tc := range []struct {
		connections, colos, want string
	}{
		{"9", "", "expected_connections must be 1..8"},
		{"0", "", "expected_connections must be 1..8"},
		{"four", "", "Cloudflared expected connections must be a whole number"},
		{"", "hk!", "expected_colos must be 3..8 letters or digits"},
		{"", "hkg, HKG", "expected_colos must be 3..8 letters or digits, without duplicates"},
		{"", "aaa,bbb,ccc,ddd,eee,fff,ggg,hhh,iii", "expected_colos accepts at most 8 colos"},
	} {
		m, _ := editorModel(t)
		before := len(m.Edit.Config.Checks)
		m.openForm("check", "")
		f := m.modal.form
		for label, value := range map[string]string{
			"ID": "home-tunnel", "Kind": "cloudflared", "Target": "http://127.0.0.1:20241",
			"Cloudflared expected connections": tc.connections, "Cloudflared expected colos": tc.colos,
		} {
			f.get(label).value = value
		}
		m.Key("ctrl-s")
		if m.modal == nil || !strings.Contains(m.modal.form.err, tc.want) {
			t.Errorf("connections %q colos %q: not refused with %q", tc.connections, tc.colos, tc.want)
		}
		if len(m.Edit.Config.Checks) != before {
			t.Errorf("connections %q colos %q: a refused form changed the draft", tc.connections, tc.colos)
		}
	}
}

// Leaving the connector kind clears its options, which another kind of
// check would otherwise carry without anything reading them.
func TestEditorCloudflaredOptionsGoWithTheKind(t *testing.T) {
	m, _ := editorModel(t)
	c := m.Edit.Config
	x := &c.Checks[0]
	x.Kind, x.Target, x.Endpoint, x.Path, x.Service = "cloudflared", "http://127.0.0.1:20241", "", "", ""
	x.HTTP = model.HTTPOptions{}
	two := 2
	x.Adapter.Cloudflared = &model.CloudflaredOptions{ExpectedConnections: &two, ExpectedColos: []string{"hkg"}}
	m.Edit.Config = c
	m.rebuild()
	m.openForm("check", x.ID)
	f := m.modal.form
	f.get("Kind").value, f.get("Target").value = "tcp", "127.0.0.1:20241"
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatalf("form did not save: %v", m.modal.form.err)
	}
	got := m.Edit.Config.Checks[0]
	if got.Kind != "tcp" || got.Adapter.Cloudflared != nil {
		t.Fatalf("a tcp check carries connector options: %+v", got.Adapter)
	}
	if err := config.Validate(m.Edit.Config); err != nil {
		t.Fatal(err)
	}
}
