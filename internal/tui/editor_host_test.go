package tui

import (
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
)

func TestEditorCreatesHostCheckAndPreservesUndo(t *testing.T) {
	m, _ := editorModel(t)
	m.openForm("check", "")
	f := m.modal.form
	for label, val := range map[string]string{"ID": "host-resources", "Kind": "host", "Host disk path": "/", "Host max CPU %": "85.5", "Host max memory %": "90", "Host max disk %": "90", "Host process name": "topo", "Host min processes": "1"} {
		f.get(label).value = val
	}
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatal("host form rejected", m.modal.form.err)
	}
	x, ok := config.Check(m.Edit.Config, "host-resources")
	if !ok || x.Host == nil || x.Host.MaxCPUPercent != 85.5 || x.Host.MinProcesses != 1 {
		t.Fatal(x)
	}
	m.openForm("check", x.ID)
	m.modal.form.get("Host max CPU %").value = "70"
	m.Key("ctrl-s")
	x, _ = config.Check(m.Edit.Config, "host-resources")
	if x.Host.MaxCPUPercent != 70 {
		t.Fatal(x.Host)
	}
	m.Key("u")
	x, _ = config.Check(m.Edit.Config, "host-resources")
	if x.Host.MaxCPUPercent != 85.5 {
		t.Fatal("undo shared host options", x.Host)
	}
	m.openForm("check", x.ID)
	m.modal.form.get("Host max CPU %").value = "NaN"
	m.Key("ctrl-s")
	if m.modal == nil || m.modal.form.err == "" {
		t.Fatal("non-finite threshold accepted")
	}
}
