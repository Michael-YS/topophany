// Package secure contains small shared security primitives. Tokens are random
// bearer secrets, not passwords. Their hashes can be stored on the collector.
package secure

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

func Token() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func SecretEnv(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	s, ok := os.LookupEnv(name)
	if !ok || s == "" {
		return "", errors.New("configured secret environment variable is missing")
	}
	return s, nil
}
func Decode(r io.Reader, v any, limit int64) error {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return e
	}
	if int64(len(b)) > limit {
		return errors.New("input limit exceeded")
	}
	if e := JSONBudget(b); e != nil {
		return e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	var more any
	if e = d.Decode(&more); e != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// PrivateRead rejects symlink files and permissive POSIX modes. Windows ACLs
// are inherited from the per-user config directory and must be reviewed there.
func PrivateRead(path string) ([]byte, error) {
	s, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() {
		return nil, errors.New("credential must be a regular non-symlink file")
	}
	if runtime.GOOS != "windows" && s.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential permissions must be 0600 or stricter")
	}
	if s.Size() > 1<<20 {
		return nil, errors.New("credential file too large")
	}
	return ReadBounded(path, 1<<20)
}
func AtomicWrite(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if s, e := os.Lstat(path); e == nil && !s.Mode().IsRegular() {
		return errors.New("refusing to replace non-regular file")
	}
	f, e := os.CreateTemp(dir, ".topophany-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = replaceFile(tmp, path); e != nil {
		return e
	}
	return SyncDir(dir)
}
func SyncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func URL(raw string, insecure bool) error {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid collector URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (insecure || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("HTTPS required except literal loopback; insecure HTTP requires explicit consent")
}
func Text(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
func ErrorCode(e error) string {
	if e == nil {
		return ""
	}
	var unknownCA x509.UnknownAuthorityError
	if errors.As(e, &unknownCA) {
		return "tls_unknown_authority"
	}
	var hostname x509.HostnameError
	if errors.As(e, &hostname) {
		return "tls_name_mismatch"
	}
	var invalidCert x509.CertificateInvalidError
	if errors.As(e, &invalidCert) {
		return "tls_certificate_invalid"
	}
	var ne net.Error
	if errors.As(e, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(e, os.ErrPermission) {
		return "permission_denied"
	}
	return "network_error"
}
func CheckToken(s string) error {
	if len(s) < 32 || len(s) > 512 || strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return fmt.Errorf("token must be 32..512 characters without control delimiters")
	}
	return nil
}

// ReadBounded checks a regular file before reading and still limits the stream
// if its size changes concurrently. It must not block on FIFOs/devices.
func ReadBounded(path string, limit int64) ([]byte, error) {
	st, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("expected regular file")
	}
	if st.Size() > limit {
		return nil, errors.New("file limit exceeded")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("file limit exceeded")
	}
	return b, nil
}
