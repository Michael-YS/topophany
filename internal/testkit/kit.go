// Package testkit contains hermetic test fixtures, not application runtime code.
package testkit

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Config() model.Config {
	c := model.Config{Schema: 1, Project: "lab", Nodes: []model.Node{{ID: "home", Name: "Windows", Group: "Home"}, {ID: "wsl", Name: "WSL", Parent: "home", Group: "Home"}, {ID: "dmit", Name: "DMIT", Group: "Cloud"}, {ID: "aliyun", Name: "Aliyun", Group: "Cloud"}, {ID: "s2", Name: "S2", Group: "Cloud"}, {ID: "aaitr", Name: "Residential exit", Group: "Cloud"}, {ID: "external", Name: "External provider", Kind: "external"}}, Observers: []model.Observer{{ID: "home", Name: "Home Windows native", Node: "home", Context: "Windows native; route not automatically verified", StaleSeconds: 90}, {ID: "cloud", Name: "Cloud contrast", Node: "aliyun", Context: "Cloud network namespace", StaleSeconds: 90}}, Endpoints: []model.Endpoint{{ID: "dmit-http", Node: "dmit", Address: "https://192.0.2.10/", Network: "public"}, {ID: "dmit-ssh", Node: "dmit", Address: "192.0.2.10:22", Network: "public"}, {ID: "win-tailnet", Node: "home", Address: "100.64.0.20:22", Network: "tailnet"}, {ID: "wsl-tailnet", Node: "wsl", Address: "100.64.0.20:3080", Network: "tailnet"}, {ID: "wsl-loop", Node: "wsl", Address: "http://127.0.0.1:3080/", Network: "loopback", Observer: "home"}, {ID: "forward-loop", Node: "aliyun", Address: "http://127.0.0.1:18080/", Network: "loopback", Observer: "cloud"}}, Services: []model.Service{{ID: "web", Name: "Web", Node: "dmit", Endpoints: []string{"dmit-http"}}, {ID: "dsh", Name: "dsh", Node: "wsl", Endpoints: []string{"wsl-loop", "forward-loop"}}, {ID: "notes", Name: "Unmonitored notes", Node: "wsl"}}, Links: []model.Link{{ID: "home-dmit-link", From: "home", To: "dmit", Transport: "public TCP"}, {ID: "dmit-exit-link", From: "dmit", To: "aaitr", Transport: "public proxy transport"}}, Paths: []model.Path{{ID: "direct", Name: "DMIT direct", Observer: "home", Nodes: []string{"home", "dmit"}, Links: []string{"home-dmit-link"}}, {ID: "contrast", Name: "Cloud to DMIT", Observer: "cloud", Nodes: []string{"aliyun", "dmit"}}, {ID: "primary", Name: "DMIT to residential", Observer: "home", Nodes: []string{"home", "dmit", "aaitr"}, Links: []string{"home-dmit-link", "dmit-exit-link"}}, {ID: "backup", Name: "S2 to residential", Observer: "home", Nodes: []string{"home", "s2", "aaitr"}}, {ID: "cdn", Name: "CDN to DMIT to residential", Observer: "home", Nodes: []string{"home", "external", "dmit", "aaitr"}, Opaque: true}, {ID: "residential", Name: "Residential candidates", Observer: "home", Candidates: []string{"primary", "backup", "cdn"}}}, Checks: []model.Check{{ID: "home-http", Name: "Home DMIT HTTP", Observer: "home", Endpoint: "dmit-http", Service: "web", Path: "direct", Kind: "http"}, {ID: "home-ssh", Observer: "home", Endpoint: "dmit-ssh", Path: "direct", Kind: "ssh-banner"}, {ID: "cloud-http", Observer: "cloud", Endpoint: "dmit-http", Service: "web", Path: "contrast", Kind: "http"}, {ID: "primary-http", Observer: "home", Endpoint: "dmit-http", Path: "primary", Kind: "http"}, {ID: "backup-http", Observer: "home", Endpoint: "dmit-http", Path: "backup", Kind: "http"}, {ID: "cdn-http", Observer: "home", Endpoint: "dmit-http", Path: "cdn", Kind: "http"}}}
	config.Defaults(&c)
	return c
}
func Observation(c model.Config, id string, seq uint64, at time.Time, outcome string) model.Observation {
	x, _ := config.Check(c, id)
	proof := "http_response"
	if x.Kind == "ssh-banner" {
		proof = "ssh_banner"
	}
	connected := outcome == "ok" || outcome == "policy"
	if !connected {
		proof = ""
	}
	return model.Observation{ID: x.Observer + ".boot." + itoa(seq), Project: c.Project, Revision: config.Revision(c), Check: id, Observer: x.Observer, Boot: "boot", Sequence: seq, SampledAt: at, Outcome: outcome, Evidence: "measured", Stage: "test", Detail: "fixture evidence", DurationMS: 1, Connectivity: connected, PathEvidence: "requested_not_route_verified", Facts: map[string]string{"response_proof": proof}}
}
func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
func Stream(t testing.TB, handler func(net.Conn)) string {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			active[c] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				defer func() { mu.Lock(); delete(active, c); mu.Unlock() }()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				handler(c)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for c := range active {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String()
}
func Relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(a, b); a.Close(); b.Close() }()
	go func() { defer wg.Done(); _, _ = io.Copy(b, a); a.Close(); b.Close() }()
	wg.Wait()
}
func Certificate(t testing.TB, names ...string) (cert, key, ca string) {
	t.Helper()
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Topophany local test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: names, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if e != nil {
		t.Fatal(e)
	}
	pk, e := x509.MarshalECPrivateKey(private)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cert = filepath.Join(dir, "cert.pem")
	key = filepath.Join(dir, "key.pem")
	if e = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: pk}), 0600); e != nil {
		t.Fatal(e)
	}
	return cert, key, cert
}
func Wait(t testing.TB, timeout time.Duration, f func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if f() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("condition did not become true before deadline")
			return
		case <-tick.C:
		}
	}
}
