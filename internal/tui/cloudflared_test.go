package tui

import (
	"slices"
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
