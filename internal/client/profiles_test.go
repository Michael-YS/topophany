package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestListProfilesMetadataAndConnectionValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	c := Credentials{Schema: 1, Active: "home", Profiles: map[string]Profile{
		"work": {Project: "", Servers: []Endpoint{{URL: "https://private.invalid/user", Token: "private-token"}}},
		"home": {Project: "homelab", Servers: []Endpoint{{URL: "not a URL", TokenEnv: "TOPO_TEST_UNSET_DISCOVERY_TOKEN"}}},
		"aaa":  {Project: "other"},
	}}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	want := []ProfileSummary{{Name: "home", Project: "homelab", Active: true}, {Name: "aaa", Project: "other"}, {Name: "work"}}
	for i := 0; i < 3; i++ {
		got, err := ListProfiles(path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, err)
		}
		raw, err := json.Marshal(got)
		if err != nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "TOKEN") || strings.Contains(string(raw), "servers") {
			t.Fatal("metadata leaked connection details", string(raw), err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("discovery changed credentials")
	}
	if _, err := Load(path); err == nil {
		t.Fatal("discovery relaxed connection validation")
	}
}

func TestListProfilesBoundKeepsActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	c := Credentials{Schema: 1, Active: "z-active", Profiles: map[string]Profile{"z-active": {Project: "lab"}}}
	for i := 0; i < 70; i++ {
		c.Profiles[fmt.Sprintf("p%02d", i)] = Profile{Project: "lab"}
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := ListProfiles(path)
	if err != nil || len(got) != 64 || got[0].Name != c.Active || !got[0].Active || got[1].Name != "p00" || got[63].Name != "p62" {
		t.Fatal(got, err)
	}
	for _, p := range got[1:] {
		if p.Active {
			t.Fatal("multiple active profiles", got)
		}
	}
}

func TestListProfilesEmptyAndInvalidFiles(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		bad           bool
	}{
		{"empty map", `{"schema":1,"profiles":{}}`, false},
		{"no profiles", `{"schema":1}`, false},
		{"invalid schema", `{"schema":2,"profiles":{}}`, true},
		{"invalid JSON", `{"schema":1,"profiles":`, true},
		{"trailing JSON", `{"schema":1} {}`, true},
		{"unknown field", `{"schema":1,"extra":true}`, true},
		{"invalid name", `{"schema":1,"active":"ok","profiles":{"ok":{},"bad name":{}}}`, true},
		{"invalid project", `{"schema":1,"active":"ok","profiles":{"ok":{"project":"bad project"}}}`, true},
		{"missing active", `{"schema":1,"active":"absent","profiles":{"ok":{}}}`, true},
		{"invalid active", `{"schema":1,"active":"bad name","profiles":{"ok":{}}}`, true},
		{"over budget", strings.Repeat(" ", (1<<20)+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials.json")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := ListProfiles(path)
			if (err != nil) != tc.bad || !tc.bad && (got == nil || len(got) != 0) {
				t.Fatal(got, err)
			}
		})
	}
	if _, err := ListProfiles(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestListProfilesPrivateFileBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"schema":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ListProfiles(path); err == nil {
			t.Fatal("permissive credentials accepted")
		}
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlink creation unavailable:", err)
	}
	if _, err := ListProfiles(link); err == nil {
		t.Fatal("symlink credentials accepted")
	}
	if _, err := ListProfiles(t.TempDir()); err == nil {
		t.Fatal("directory accepted as credentials")
	}
}
