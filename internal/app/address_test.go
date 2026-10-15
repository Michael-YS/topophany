package app

import (
	"bytes"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddressChangeDryRunAndReferencedDeletion(t *testing.T) {
	c := testkit.Config()
	c.Addresses = map[string]string{"entry": "192.0.2.10"}
	c.Endpoints[0].Address = "https://${entry}/"
	path := filepath.Join(t.TempDir(), "topology.json")
	original, _ := json.Marshal(c)
	os.WriteFile(path, original, 0600)
	out := &bytes.Buffer{}
	a := &App{Out: out, Err: &bytes.Buffer{}}
	if e := a.address([]string{"--config", path, "--set", "entry=192.0.2.11", "--dry-run"}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, original) {
		t.Fatal("dry run changed file")
	}
	if !strings.Contains(out.String(), "home-http") {
		t.Fatal(out.String())
	}
	out.Reset()
	if e := a.address([]string{"--config", path, "--set", "entry=192.0.2.11"}); e != nil {
		t.Fatal(e)
	}
	got, e := config.Load(path)
	if e != nil || got.Addresses["entry"] != "192.0.2.11" || got.Endpoints[0].Address != "https://${entry}/" {
		t.Fatal(e, got.Endpoints[0])
	}
	before, _ := os.ReadFile(path)
	if e = a.address([]string{"--config", path, "--delete", "entry"}); e == nil {
		t.Fatal("removed an address in use")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed deletion changed config")
	}
}
