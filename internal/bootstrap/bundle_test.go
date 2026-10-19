package bootstrap

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func options(t *testing.T, role string) Options {
	t.Helper()
	base := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c := testkit.Config()
	return Options{Role: role, Platform: runtime.GOOS, User: "topophany", InstallDir: filepath.Join(base, "runtime"), DataDir: filepath.Join(base, "data"), Server: "https://127.0.0.1:7447", Config: c, Binary: exe}
}
func TestServerBundleScopesKeysAndCertificates(t *testing.T) {
	o := options(t, "server")
	o.Observer = "home"
	o.TLSHosts = []string{"192.168.1.2"}
	dir := filepath.Join(t.TempDir(), "bundle")
	m, e := Generate(dir, o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = loadManifest(dir); e != nil {
		t.Fatal(e)
	}
	if _, e = Generate(dir, o); e == nil {
		t.Fatal("overwrote existing private bundle")
	}
	var auth api.Auth
	b, _ := os.ReadFile(filepath.Join(dir, "payload/auth.json"))
	json.Unmarshal(b, &auth)
	cred, e := client.Load(filepath.Join(dir, "enrollment/cloud/credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	token := cred.Profiles[cred.Active].Servers[0].Token
	if len(auth.Tokens) != 2 || auth.Tokens[1].Role != "worker" || auth.Tokens[1].Observer != "cloud" || auth.Tokens[1].TokenSHA256 != secure.Hash(token) {
		t.Fatal(auth)
	}
	if _, e = os.Stat(filepath.Join(dir, "enrollment/home")); !os.IsNotExist(e) {
		t.Fatal("duplicate local observer enrollment")
	}
	for _, f := range m.Files {
		if strings.Contains(f.Name, "ca-key") {
			t.Fatal("signing key installed into runtime")
		}
	}
	leafBytes, _ := os.ReadFile(filepath.Join(dir, "payload/server.pem"))
	block, _ := pem.Decode(leafBytes)
	leaf, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	rootBytes, _ := os.ReadFile(filepath.Join(dir, "payload/ca.pem"))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(rootBytes)
	for _, name := range []string{"127.0.0.1", "192.168.1.2"} {
		if _, e = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); e != nil {
			t.Fatal(e)
		}
	}
}
func clientBundle(t *testing.T) (string, Options) {
	t.Helper()
	o := options(t, "client")
	ca, _, _, _, e := certificates([]string{"localhost"})
	if e != nil {
		t.Fatal(e)
	}
	o.CA = ca
	o.Profile = client.Profile{Project: o.Config.Project, Servers: []client.Endpoint{{URL: o.Server, Token: strings.Repeat("a", 64)}}}
	dir := filepath.Join(t.TempDir(), "client")
	if _, e = Generate(dir, o); e != nil {
		t.Fatal(e)
	}
	return dir, o
}

func reviewReceipt(t *testing.T, dir string) string {
	t.Helper()
	_, digest, err := Review(dir)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestInstallRequiresUnchangedReviewReceipt(t *testing.T) {
	for _, change := range []string{"missing receipt", "payload and checksum", "destination"} {
		t.Run(change, func(t *testing.T) {
			dir, o := clientBundle(t)
			receipt := reviewReceipt(t, dir)
			m, err := loadManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing receipt":
				receipt = ""
			case "payload and checksum":
				f := &m.Files[0]
				data := []byte("attacker replacement")
				if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(f.Source)), data, 0600); err != nil {
					t.Fatal(err)
				}
				f.SHA256 = secure.Hash(string(data))
			case "destination":
				m.InstallDir = filepath.Join(t.TempDir(), "other")
			}
			raw, _ := json.Marshal(m)
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Install(context.Background(), dir, false, receipt); err == nil {
				t.Fatal("unreviewed installation accepted")
			}
			if _, err := os.Stat(o.InstallDir); !os.IsNotExist(err) {
				t.Fatal("rejected installation mutated destination", err)
			}
		})
	}
}
func TestClientInstallDryRunAndRollback(t *testing.T) {
	dir, o := clientBundle(t)
	ctx := context.Background()
	if _, _, e := Install(ctx, dir, true, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(o.InstallDir); !os.IsNotExist(e) {
		t.Fatal("dry run wrote runtime")
	}
	os.MkdirAll(o.InstallDir, 0700)
	old := []byte("previous credentials")
	target := filepath.Join(o.InstallDir, "credentials.json")
	os.WriteFile(target, old, 0600)
	_, backup, e := Install(ctx, dir, false, reviewReceipt(t, dir))
	if e != nil {
		t.Fatal(e)
	}
	c, e := client.Load(target)
	if e != nil || c.Profiles[c.Active].Project != o.Config.Project {
		t.Fatal(e, c)
	}
	if e = Rollback(ctx, backup); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(target)
	if string(b) != string(old) {
		t.Fatal("previous credentials not restored")
	}
}
func TestBundleTamperingRefused(t *testing.T) {
	dir, _ := clientBundle(t)
	payload := filepath.Join(dir, "payload/credentials.json")
	os.WriteFile(payload, []byte("tampered"), 0600)
	if _, _, e := Install(context.Background(), dir, true, ""); e == nil {
		t.Fatal("tampered enrollment accepted")
	}
}
func TestNativeServiceArtifacts(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		o := options(t, "worker")
		o.Platform = platform
		o.Observer = "home"
		o.InstallDir = "/opt/topophany path"
		o.DataDir = "/var/lib/topophany"
		if platform == "windows" {
			o.InstallDir = "C:/Users/Test User/Topophany"
			o.DataDir = "C:/Users/Test User/Topophany/data"
		}
		name, file, b, e := service(o)
		if e != nil || name == "" {
			t.Fatal(e)
		}
		switch platform {
		case "linux":
			if !strings.Contains(string(b), "User=topophany") || !strings.Contains(string(b), "--observer") {
				t.Fatal(string(b))
			}
		case "darwin":
			if !strings.Contains(string(b), "<key>UserName</key><string>topophany</string>") || !strings.HasSuffix(file, ".plist") {
				t.Fatal(string(b))
			}
		case "windows":
			var x map[string]string
			if e = json.Unmarshal(b, &x); e != nil || x["executable"] != "C:/Users/Test User/Topophany/bin/topo.exe" {
				t.Fatal(e, x)
			}
		}
	}
}

func TestInvalidRollbackCannotTouchUnrelatedFiles(t *testing.T) {
	dir, o := clientBundle(t)
	_, backup, e := Install(context.Background(), dir, false, reviewReceipt(t, dir))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(backup, "rollback.json"))
	var r installation
	json.Unmarshal(b, &r)
	unrelated := filepath.Join(t.TempDir(), "unrelated")
	os.WriteFile(unrelated, []byte("keep"), 0600)
	r.Files[0].Target = unrelated
	raw, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(backup, "rollback.json"), raw, 0600)
	if e = Rollback(context.Background(), backup); e == nil {
		t.Fatal("invalid rollback accepted")
	}
	b, _ = os.ReadFile(unrelated)
	if string(b) != "keep" {
		t.Fatal("unrelated file changed")
	}
	_ = o
}

func TestEnrollmentVerificationChecksTLSProjectAndObserver(t *testing.T) {
	o := options(t, "worker")
	o.Observer = "home"
	reportedObserver := "home"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/worker" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			w.WriteHeader(403)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"project": o.Config.Project, "observer": reportedObserver})
	}))
	defer server.Close()
	o.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	o.Profile = client.Profile{Project: o.Config.Project, Servers: []client.Endpoint{{URL: server.URL, Token: strings.Repeat("a", 64)}}}
	dir := filepath.Join(t.TempDir(), "worker")
	if _, e := Generate(dir, o); e != nil {
		t.Fatal(e)
	}
	if e := Verify(context.Background(), dir); e != nil {
		t.Fatal(e)
	}
	reportedObserver = "cloud"
	if e := Verify(context.Background(), dir); e == nil {
		t.Fatal("worker observer mismatch accepted")
	}
}
