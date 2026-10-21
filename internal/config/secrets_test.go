package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestLiteralSecretTopologyMustBePrivate(t *testing.T) {
	c := testkit.Config()
	if config.HasLiteralSecret(c) {
		t.Fatal("fixture carries a literal secret")
	}
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.json")
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(shared, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(shared); err != nil {
		t.Fatal("secret-free topology", err)
	}
	c.Checks[0].HTTP.RequestHeaders = map[string]string{"X-Key": "literal"}
	if !config.HasLiteralSecret(c) {
		t.Fatal("request header was not a literal secret")
	}
	raw, _ = json.Marshal(c)
	loose := filepath.Join(dir, "loose.json")
	if err := os.WriteFile(loose, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(loose); err == nil {
		t.Fatal("group-readable secret topology accepted")
	}
	tight := filepath.Join(dir, "tight.json")
	if err := os.WriteFile(tight, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(tight); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(tight, link); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(link); err == nil {
		t.Fatal("symlink to a secret topology accepted")
	}
	c.Checks[0].HTTP.RequestHeaders = nil
	c.Checks[0].Target = "https://user:password@192.0.2.10/"
	if !config.HasLiteralSecret(c) {
		t.Fatal("url userinfo was not a literal secret")
	}
	c.Checks[0].Target = "https://192.0.2.10/?token=secret"
	if !config.HasLiteralSecret(c) {
		t.Fatal("url query was not a literal secret")
	}
}
