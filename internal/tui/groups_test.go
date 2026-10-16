package tui

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"strings"
	"testing"
)

func businessFixture() Model {
	m := fixtureModel()
	m.GroupsCollapsed = true
	m.Snapshot.Topology.Applications = []model.Application{{ID: "shop", Name: "商城"}}
	for i := range m.Snapshot.Topology.Services {
		m.Snapshot.Topology.Services[i].Application = "shop"
		m.Snapshot.Topology.Services[i].Environment = "production"
	}
	m.setTab(tabServices)
	return m
}
func TestBusinessFoldsSearchAndCrossTab(t *testing.T) {
	m := businessFixture()
	if len(m.Rows()) != 1 || m.Rows()[0].Kind != "group" {
		t.Fatal(m.Rows())
	}
	m.Key("enter")
	if len(m.Rows()) < 2 || m.Detail {
		t.Fatal("Enter must expand group")
	}
	m.Selected = 1
	id := m.Rows()[1].ID
	m.Key("c")
	if m.Tab != tabChecks || m.Application != "shop" || m.Environment != "production" || m.ServiceFilter != id {
		t.Fatal("lost service scope")
	}
	m.selectEntity("service", id)
	if r, ok := m.selectedRow(); !ok || r.ID != id {
		t.Fatal("entity link failed to reveal folded target")
	}
	m.ServiceFilter = ""
	m.Expanded = nil
	m.Search = "商城"
	if len(m.Rows()) < 2 {
		t.Fatal("search did not reveal matching children")
	}
	m.Search = ""
	if len(m.Rows()) != 1 {
		t.Fatal("search changed remembered folds")
	}
}
func TestBusinessBrowseMenuAndRendering(t *testing.T) {
	m := businessFixture()
	m.Key("b")
	m.Key("enter")
	if m.modal == nil || m.modal.title != "Application" {
		t.Fatal("nested menu closed")
	}
	m.Key("end")
	m.Key("esc")
	for _, w := range []int{40, 80, 120, 160} {
		m.Width = w
		m.Height = 28
		cv := m.Render()

		plain := strings.Join(cv, "\n")
		if !strings.Contains(plain, "SERVICES") {
			t.Fatal("missing services title")
		}
	}
}

func TestApplicationGroupChecksAndEvidence(t *testing.T) {
	m := businessFixture()
	m.Key("i")
	if !m.Detail {
		t.Fatal("group evidence inaccessible")
	}
	m.Key("esc")
	m.Key("c")
	if m.Tab != tabChecks || m.Application != "shop" || m.Environment != "production" {
		t.Fatal("application drilldown lost scope")
	}
	m.Key("b")
	m.modal.pick(4)
	m.modal = nil
	if m.Application != "" || m.Environment != "" || m.ServiceFilter != "" {
		t.Fatal("clear filters failed")
	}
}

func TestApplicationEditorLifecycle(t *testing.T) {
	m, _ := editorModel(t)
	m.openForm("application", "")
	f := m.modal.form
	for _, field := range f.fields {
		if field.label == "ID" {
			field.value = "shop"
		}
		if field.label == "Name" {
			field.value = "Shop"
		}
	}
	if err := m.Edit.Apply(f.commit); err != nil {
		t.Fatal(err)
	}
	m.rebuild()
	m.openForm("service", m.Edit.Config.Services[0].ID)
	f = m.modal.form
	for _, field := range f.fields {
		if field.label == "Application" {
			field.value = "shop"
		}
		if field.label == "Environment" {
			field.value = "production"
		}
	}
	if err := m.Edit.Apply(f.commit); err != nil {
		t.Fatal(err)
	}
	if err := m.Edit.Apply(func(c *model.Config) error { return remove(c, "application", "shop") }); err == nil {
		t.Fatal("deleted referenced application")
	}
	if !m.Edit.Undo() {
		t.Fatal("cannot undo scope assignment")
	}
	if err := m.Edit.Apply(func(c *model.Config) error { return remove(c, "application", "shop") }); err != nil {
		t.Fatal(err)
	}
}
