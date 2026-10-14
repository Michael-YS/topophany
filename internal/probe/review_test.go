package probe

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestDNSRejectsLabelBoundarySpoof(t *testing.T) {
	q, _ := dnsQuery(42, "test.example", 1)
	b := dnsAnswer(q)
	binary.BigEndian.PutUint16(b[6:], 1)
	// One label containing a dot must not equal two labels test + example.
	b = append(b, 12)
	b = append(b, "test.example"...)
	b = append(b, 0, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 203, 0, 113, 8)
	if ips, e := parseDNS(b, 42, "test.example", 1); e == nil && len(ips) > 0 {
		t.Fatal("unrelated DNS owner accepted", ips)
	}
}

func TestDNSRejectsTrailingCNAMEData(t *testing.T) {
	q, _ := dnsQuery(42, "test.example", 1)
	b := dnsAnswer(q)
	binary.BigEndian.PutUint16(b[6:], 1)
	b = append(b, 0xc0, 0x0c, 0, 5, 0, 1, 0, 0, 0, 60, 0, 3, 0, 'x', 'y')
	if _, e := parseDNS(b, 42, "test.example", 1); e == nil {
		t.Fatal("CNAME with trailing garbage accepted")
	}
}

func TestDNSLargeAnswerProducesDeliverableError(t *testing.T) {
	server := dnsFixture(t, true, func(q []byte) []byte {
		ips := make([]string, 300)
		for i := range ips {
			ips[i] = "203.0.113.8"
		}
		return dnsAnswer(q, ips...)
	})
	x := httpCheck(server)
	x.Kind = "dns"
	x.TimeoutSeconds = 5
	x.DNS = model.DNSOptions{Name: "test.example", Type: "A"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || o.Code != "address_evidence_budget_exceeded" || len(o.Facts["addresses"]) > 2048 {
		t.Fatal(o)
	}
}

func TestSafeFactRespectsByteBudget(t *testing.T) {
	for _, value := range []string{strings.Repeat("😀", 512), strings.Repeat("😀", 513), strings.Repeat("中", 900)} {
		got := safeFact(value)
		if len(got) > 2048 || !utf8.ValidString(got) {
			t.Fatal("invalid fact budget", len(got))
		}
	}
}

func TestSSHKnownHostsOptionParsesAsOneLiteralFile(t *testing.T) {
	bin, e := exec.LookPath("ssh")
	if e != nil {
		t.Skip("OpenSSH unavailable")
	}
	path := filepath.Join(t.TempDir(), "known hosts")
	encoded, e := sshFile(path)
	if e != nil {
		t.Fatal(e)
	}
	// -G only prints configuration; no connection is made.
	out, e := exec.Command(bin, "-G", "-F", os.DevNull, "-o", "UserKnownHostsFile=\""+encoded+"\"", "example.invalid").Output()
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(out), "userknownhostsfile "+filepath.ToSlash(path)) {
		t.Fatal("OpenSSH did not preserve the known_hosts path")
	}
	for _, p := range []string{"${HOME}/known", "~/known", "bad\"quote", "bad\nline"} {
		if _, e := sshFile(p); e == nil {
			t.Fatal("unsafe path accepted", p)
		}
	}
}

func TestMappedIPv4ExitMatchesIPv4CIDR(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "::ffff:203.0.113.8") }))
	defer s.Close()
	x := httpCheck(s.URL)
	x.HTTP.ExpectedExit = []string{"203.0.113.0/24"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" {
		t.Fatal(o)
	}
}

func TestAdapterURLFragmentsAreRedacted(t *testing.T) {
	if strings.Contains(publicURL("https://user:pass@example.invalid/test?token=secret#private"), "private") {
		t.Fatal("fragment leaked")
	}
}
