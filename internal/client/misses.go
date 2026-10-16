package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Winsock reports socket failures with WSA error codes. On Windows, Go's
// syscall.E* values are separate, invented values rather than those codes.
const (
	wsaNetUnreachable  syscall.Errno = 10051
	wsaConnReset       syscall.Errno = 10054
	wsaConnRefused     syscall.Errno = 10061
	wsaHostUnreachable syscall.Errno = 10065
)

// misses gathers why each collector a read passed over gave no answer, so
// that the error saying none did also says why: on a first connection the
// reason (refused, a certificate from an authority not trusted, a name
// that does not resolve) is the whole story.
type misses []string

func (m *misses) add(ep Endpoint, why string) {
	host := ep.URL
	if u, e := url.Parse(ep.URL); e == nil && u.Host != "" {
		host = u.Host
	}
	*m = append(*m, host+": "+why)
}

// String lists the reasons in parentheses, "" when there are none. A
// reason holds no parenthesis of its own, so the list can be read back.
func (m misses) String() string {
	if len(m) == 0 {
		return ""
	}
	return " (" + strings.Join(m, "; ") + ")"
}

// unreachable says in a few words why a request got no response.
func unreachable(e error) string {
	var unknown x509.UnknownAuthorityError
	var name x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(e, &unknown):
		return "TLS certificate from an authority not trusted here — see --ca-file"
	case errors.As(e, &name):
		return "TLS certificate is for another name"
	case errors.As(e, &invalid) && invalid.Reason == x509.Expired:
		return "TLS certificate expired or not yet valid"
	case errors.As(e, &invalid):
		return "TLS certificate not valid for a collector"
	case errors.As(e, &record), strings.Contains(e.Error(), "server gave HTTP response to HTTPS client"):
		// net/http turns the record error into this sentence.
		return "not answering in TLS — is it an http:// collector?"
	case errors.As(e, &dns):
		return "host name not found"
	case errors.Is(e, syscall.ECONNREFUSED), windowsSocketError(e, wsaConnRefused):
		return "connection refused — is the collector running?"
	case errors.Is(e, syscall.EHOSTUNREACH), errors.Is(e, syscall.ENETUNREACH),
		windowsSocketError(e, wsaHostUnreachable), windowsSocketError(e, wsaNetUnreachable):
		return "network unreachable"
	case errors.Is(e, syscall.ECONNRESET), windowsSocketError(e, wsaConnReset):
		return "connection reset"
	case errors.As(e, &ne) && ne.Timeout():
		return "timed out"
	}
	return "unreachable"
}

func windowsSocketError(e error, code syscall.Errno) bool {
	return runtime.GOOS == "windows" && errors.Is(e, code)
}

// unavailable says why a collector that responded is passed over.
func unavailable(status int) string {
	if status == 429 {
		return "busy, HTTP 429"
	}
	return "answered HTTP " + strconv.Itoa(status)
}
