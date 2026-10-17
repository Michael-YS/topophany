package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTemporaryCredentialsNeverRequireSecretArguments(t *testing.T) {
	token := strings.Repeat("c", 64)
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("temporary credential did not reach Authorization")
		}
		w.WriteHeader(503)
	}))
	defer s.Close()
	for _, args := range [][]string{{"--credential", token}, {"--credential=" + token}} {
		out, stderr, e := invoke("", append(args, "--json")...)
		if e == nil || strings.Contains(out+stderr+e.Error(), token) || hits.Load() != 0 {
			t.Fatal("raw credential accepted or printed")
		}
	}
	base := []string{"--credentials", filepath.Join(t.TempDir(), "missing"), "--server", s.URL, "--project", "lab"}
	for _, mode := range []string{"stdin", "file"} {
		t.Run(mode, func(t *testing.T) {
			args := append([]string(nil), base...)
			if mode == "stdin" {
				args = append(args, "--token-stdin")
			} else {
				args = append(args, "--credential-file", credentialFile(t, token+"\n"))
			}
			out, stderr, e := invoke(token+"\n", append(args, "--json")...)
			if e == nil {
				t.Fatal("503 fixture unexpectedly answered a snapshot")
			}
			if strings.Contains(out+stderr+e.Error(), token) {
				t.Fatal("temporary credential appeared in output")
			}
		})
	}
	if hits.Load() != 2 {
		t.Fatal("safe overrides did not both reach the collector", hits.Load())
	}
}

func TestTemporaryCredentialInputsFailBeforeDial(t *testing.T) {
	token := strings.Repeat("c", 64)
	private := credentialFile(t, token)
	link := filepath.Join(t.TempDir(), "link")
	linkErr := os.Symlink(private, link)
	type inputCase struct {
		name, input string
		args        []string
	}
	cases := []inputCase{
		{"conflicting", token, []string{"--token-stdin", "--credential-file", private}},
		{"empty", "", []string{"--token-stdin"}},
		{"invalid", "bad", []string{"--token-stdin"}},
		{"oversized", strings.Repeat("x", 1025), []string{"--token-stdin"}},
		{"missing", "", []string{"--credential-file", filepath.Join(t.TempDir(), "missing")}},
		{"invalid-file", "", []string{"--credential-file", credentialFile(t, "bad")}},
		{"oversized-file", "", []string{"--credential-file", credentialFile(t, strings.Repeat("x", 1025))}},
	}
	if linkErr == nil {
		cases = append(cases, inputCase{"symlink", "", []string{"--credential-file", link}})
	}
	if runtime.GOOS != "windows" {
		permissive := credentialFile(t, token)
		if e := os.Chmod(permissive, 0644); e != nil {
			t.Fatal(e)
		}
		cases = append(cases, inputCase{"permissive", "", []string{"--credential-file", permissive}})
	}
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer s.Close()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--json", "--server", s.URL, "--project", "lab", "--credentials", filepath.Join(t.TempDir(), "missing")}
			out, stderr, e := invoke(tc.input, append(args, tc.args...)...)
			if e == nil || strings.Contains(out+stderr+e.Error(), token) {
				t.Fatal("unsafe input accepted or leaked")
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatal("invalid credential input reached the network", hits.Load())
	}
	// Both readers stop after the same bounded input even without EOF.
	r := bytes.NewReader([]byte(strings.Repeat("x", 2048)))
	if _, e := readToken(r); e == nil || r.Len() != 1023 {
		t.Fatal("stdin token reader exceeded its byte budget", r.Len(), e)
	}
}
