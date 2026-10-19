package app

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/bootstrap"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func deploymentBundle(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	cfg := filepath.Join(base, "topology.json")
	saveConfig(t, cfg, testkit.Config())
	dir := filepath.Join(base, "server")
	out, _, err := invoke("", "init", "--role", "server", "--config", cfg, "--dir", dir,
		"--server", "https://127.0.0.1:7447", "--observer", "home", "--user", "topophany",
		"--install-dir", filepath.Join(base, "runtime"), "--data-dir", filepath.Join(base, "data"), "--tls-hosts", "localhost,192.0.2.1")
	var m bootstrap.Manifest
	if err != nil || json.Unmarshal([]byte(out), &m) != nil || m.Role != "server" {
		t.Fatal("server setup failed", err, out)
	}
	if _, _, err := bootstrap.Review(dir); err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(filepath.Join(dir, "payload", "server.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(cert)
	if block == nil {
		t.Fatal("missing server certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost", "192.0.2.1"} {
		if err := leaf.VerifyHostname(host); err != nil {
			t.Fatal("additional TLS host missing", err)
		}
	}
	return dir, base
}

func TestDeploymentCLIClientInstallAndRollback(t *testing.T) {
	server, base := deploymentBundle(t)
	bundle, target := filepath.Join(base, "client"), filepath.Join(base, "client-runtime")
	args := []string{"init", "--role", "client", "--project", "lab", "--dir", bundle,
		"--credentials", filepath.Join(server, "payload", "credentials.json"), "--ca-file", filepath.Join(server, "payload", "ca.pem"), "--install-dir", target}
	if _, _, err := invoke("", args...); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(bundle, "payload", "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(target, "credentials.json")
	if err := os.WriteFile(installed, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := invoke("", "install", "--bundle", bundle, "--dry-run")
	var review struct {
		Receipt string `json:"bundle_sha256"`
	}
	if err != nil || json.Unmarshal([]byte(out), &review) != nil || review.Receipt == "" {
		t.Fatal("review failed", err, out)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != "previous" {
		t.Fatal("dry run mutated target", err)
	}
	out, _, err = invoke("", "install", "--bundle", bundle, "--bundle-sha256", review.Receipt)
	var result struct {
		Backup string `json:"backup"`
	}
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || result.Backup == "" {
		t.Fatal("install failed", err, out)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != string(before) {
		t.Fatal("installed credentials differ", err)
	}
	if _, _, err := invoke("", "install", "--rollback", result.Backup); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != "previous" {
		t.Fatal("rollback failed", err)
	}
}

func TestDeploymentCLIInputBoundariesAndInteractiveSetup(t *testing.T) {
	server, base := deploymentBundle(t)
	ca := filepath.Join(server, "payload", "ca.pem")
	for _, tc := range []struct {
		name, input string
		extra       []string
		want        string
	}{
		{"valid", strings.Repeat(" ", 4032) + strings.Repeat("a", 64), nil, ""},
		{"oversized", strings.Repeat("a", 4097), nil, "token input too large"},
		{"empty", "", nil, "token must"},
		{"invalid", "bad", nil, "token must"},
		{"conflicting", "", []string{"--credentials", filepath.Join(server, "payload", "credentials.json")}, "choose credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"init", "--role", "client", "--dir", filepath.Join(base, tc.name), "--project", "lab", "--server", "https://127.0.0.1:7447", "--ca-file", ca, "--install-dir", filepath.Join(base, "target-"+tc.name), "--token-stdin"}
			_, _, err := invoke(tc.input, append(args, tc.extra...)...)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("input boundary: %v", err)
			}
		})
	}
	interactive := strings.Join([]string{"client", filepath.Join(base, "interactive"), "lab", "https://127.0.0.1:7447", ca, strings.Repeat("b", 64)}, "\n")
	out, prompts, err := invoke(interactive, "init", "--interactive", "--token-stdin", "--install-dir", filepath.Join(base, "interactive-runtime"))
	if err != nil || !strings.Contains(prompts, "Role") || !strings.Contains(out, `"role": "client"`) {
		t.Fatal("interactive setup failed", err, prompts, out)
	}
}

func TestInstallCLIRejectsRollbackCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"--bundle", "bundle"}, {"--dry-run"}, {"--verify"}, {"--bundle-sha256", "receipt"},
	} {
		_, _, err := invoke("", append([]string{"install", "--rollback", "backup"}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "rollback cannot be combined") {
			t.Fatal("conflicting rollback flags accepted", args, err)
		}
	}
}

func TestDeploymentCLIWorkerEnrollmentReview(t *testing.T) {
	server, base := deploymentBundle(t)
	dir := filepath.Join(base, "worker")
	_, _, err := invoke("", "init", "--role", "worker", "--dir", dir, "--observer", "cloud",
		"--config", filepath.Join(base, "topology.json"), "--credentials", filepath.Join(server, "enrollment", "cloud", "credentials.json"),
		"--ca-file", filepath.Join(server, "enrollment", "cloud", "ca.pem"), "--user", "topophany",
		"--install-dir", filepath.Join(base, "worker-runtime"), "--data-dir", filepath.Join(base, "worker-data"))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := invoke("", "install", "--bundle", dir, "--dry-run")
	var result struct {
		Manifest bootstrap.Manifest `json:"manifest"`
		Receipt  string             `json:"bundle_sha256"`
	}
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || result.Manifest.Role != "worker" || result.Manifest.Observer != "cloud" || result.Receipt == "" {
		t.Fatal("worker enrollment review failed", err, out)
	}
}
