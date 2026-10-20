package tui

import (
	"slices"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestEditorCloudflareKindKeepsNestedOptions(t *testing.T) {
	if !slices.Contains(checkKinds, "cloudflare") {
		t.Fatal("cloudflare is not offered by the check form")
	}
	m, _ := editorModel(t)
	c := m.Edit.Config
	x := &c.Checks[0]
	x.Kind, x.Target, x.Endpoint, x.Path, x.Service = "cloudflare", "https://api.cloudflare.com/client/v4", "", "", ""
	x.IntervalSeconds = 60
	x.HTTP = model.HTTPOptions{}
	x.Adapter.TokenEnv = "CF_API_TOKEN"
	four := 4
	x.Adapter.Cloudflare = &model.CloudflareOptions{
		Read:                "tunnel",
		AccountID:           "11111111111111111111111111111111",
		TunnelID:            "22222222222222222222222222222222",
		ExpectedConnections: &four,
		ExpectedColos:       []string{"HKG", "NRT"},
	}
	m.Edit.Config = c
	m.rebuild()
	m.openForm("check", x.ID)
	f := m.modal.form
	if f.val("Kind") != "cloudflare" || !slices.Contains(f.get("Kind").choices, "cloudflare") {
		t.Fatalf("form did not start on cloudflare: %q", f.val("Kind"))
	}
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatalf("form did not save: %v", m.modal.form.err)
	}
	if err := config.Validate(m.Edit.Config); err != nil {
		t.Fatal(err)
	}
	got := m.Edit.Config.Checks[0].Adapter.Cloudflare
	if got == nil || got.Read != "tunnel" || got.ExpectedConnections == nil || *got.ExpectedConnections != 4 || !slices.Equal(got.ExpectedColos, []string{"HKG", "NRT"}) {
		t.Fatalf("unlisted nested options lost: %+v", got)
	}
}

func TestEditorCloudflareCreateFromForm(t *testing.T) {
	m, _ := editorModel(t)
	m.openForm("check", "")
	f := m.modal.form
	for label, value := range map[string]string{
		"ID":                              "cf-api",
		"Kind":                            "cloudflare",
		"Cloudflare token env":            "CF_API_TOKEN",
		"Cloudflare read":                 "tunnel",
		"Cloudflare account id":           "11111111111111111111111111111111",
		"Cloudflare tunnel id":            "22222222222222222222222222222222",
		"Cloudflare expected connections": "4",
		"Cloudflare expected colos":       "HKG, NRT",
	} {
		f.get(label).value = value
	}
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatalf("cloudflare form rejected: %v", m.modal.form.err)
	}
	x, ok := config.Check(m.Edit.Config, "cf-api")
	if !ok || x.Kind != "cloudflare" || x.Target != "" || x.Adapter.TokenEnv != "CF_API_TOKEN" || x.Adapter.Cloudflare == nil {
		t.Fatalf("check not created: %+v", x)
	}
	cf := x.Adapter.Cloudflare
	if cf.Read != "tunnel" || cf.AccountID != "11111111111111111111111111111111" || cf.TunnelID != "22222222222222222222222222222222" || cf.ExpectedConnections == nil || *cf.ExpectedConnections != 4 || !slices.Equal(cf.ExpectedColos, []string{"HKG", "NRT"}) {
		t.Fatalf("nested options %+v", cf)
	}
	if err := config.Validate(m.Edit.Config); err != nil {
		t.Fatal(err)
	}

	// The same form authors the DNS read; fields from the tunnel read are
	// cleared so validation sees only the selected read's declaration.
	m.openForm("check", "")
	f = m.modal.form
	for label, value := range map[string]string{
		"ID":                          "cf-dns",
		"Kind":                        "cloudflare",
		"Cloudflare token env":        "CF_API_TOKEN",
		"Cloudflare read":             "dns_record",
		"Cloudflare zone id":          "33333333333333333333333333333333",
		"Cloudflare record name":      "www.example.com",
		"Cloudflare record type":      "A",
		"Cloudflare expected proxied": "true",
		"Cloudflare expected content": "198.51.100.7",
	} {
		f.get(label).value = value
	}
	m.Key("ctrl-s")
	if m.modal != nil {
		t.Fatalf("dns form rejected: %v", m.modal.form.err)
	}
	x, _ = config.Check(m.Edit.Config, "cf-dns")
	cf = x.Adapter.Cloudflare
	if cf == nil || cf.Read != "dns_record" || cf.ZoneID != "33333333333333333333333333333333" || cf.ExpectedProxied == nil || !*cf.ExpectedProxied || cf.ExpectedContent != "198.51.100.7" || cf.AccountID != "" || cf.TunnelID != "" {
		t.Fatalf("dns options %+v", cf)
	}
	if err := config.Validate(m.Edit.Config); err != nil {
		t.Fatal(err)
	}
}
