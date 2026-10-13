package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type failure struct {
	stage, code string
	local       bool
}

func (e *failure) Error() string { return e.stage + ": " + e.code }
func fail(stage string, e error) error {
	var se *os.SyscallError
	if errors.As(e, &se) && (se.Syscall == "bind" || se.Syscall == "socket" || se.Syscall == "setsockopt") {
		return &failure{stage: "setup", code: "local_" + se.Syscall + "_failed", local: true}
	}
	if errors.Is(e, os.ErrPermission) {
		return &failure{stage: "setup", code: "local_permission_denied", local: true}
	}
	return &failure{stage: stage, code: secure.ErrorCode(e)}
}
func local(stage, code string) error { return &failure{stage: stage, code: code, local: true} }
func TLSConfig(name, caFile string) (*tls.Config, error) {
	c := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name}
	if caFile != "" {
		b, e := secure.ReadBounded(caFile, 1<<20)
		if e != nil {
			return nil, local("setup", "ca_file_unreadable")
		}
		if len(b) > 1<<20 {
			return nil, local("setup", "ca_file_too_large")
		}
		pool, e := x509.SystemCertPool()
		if e != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(b) {
			return nil, local("setup", "ca_file_invalid")
		}
		c.RootCAs = pool
	}
	return c, nil
}

type Dialer struct {
	Proxies []model.Proxy
	BindIP  string
}

// DialContext creates a new stream for every call. A proxy core beyond this
// stream can still multiplex its own transport; this does not prove a new
// outer Reality/XHTTP connection was created.
func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, local("setup", "stream_transport_required")
	}
	for i, p := range d.Proxies {
		if config.ProxyCredentials(p, i) != nil {
			return nil, local("setup", "insecure_proxy_credentials")
		}
	}
	nd := net.Dialer{}
	if d.BindIP != "" {
		nd.LocalAddr = &net.TCPAddr{IP: net.ParseIP(d.BindIP)}
	}
	first := address
	if len(d.Proxies) > 0 {
		first = d.Proxies[0].Address
	}
	var conn net.Conn
	var e error
	start := 0
	if len(d.Proxies) > 0 && d.Proxies[0].Kind == "ssh" {
		target := address
		if len(d.Proxies) > 1 {
			target = d.Proxies[1].Address
		}
		conn, e = sshDial(ctx, d.Proxies[0], target, d.BindIP)
		start = 1
	} else {
		conn, e = nd.DialContext(ctx, network, first)
		if e != nil {
			return nil, fail("connect", e)
		}
	}
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	if deadline, yes := ctx.Deadline(); yes {
		_ = conn.SetDeadline(deadline)
	}
	baseConn := conn
	stop := context.AfterFunc(ctx, func() { baseConn.Close() })
	defer stop()
	for i := start; i < len(d.Proxies); i++ {
		p := d.Proxies[i]
		dest := address
		if i+1 < len(d.Proxies) {
			dest = d.Proxies[i+1].Address
		}
		if p.Kind == "https-connect" {
			host, _, _ := net.SplitHostPort(p.Address)
			if p.ServerName != "" {
				host = p.ServerName
			}
			tc, er := TLSConfig(host, p.CAFile)
			if er != nil {
				return nil, er
			}
			t := tls.Client(conn, tc)
			if er = t.HandshakeContext(ctx); er != nil {
				return nil, fail("proxy_tls", er)
			}
			conn = t
		}
		pass, er := secure.SecretEnv(p.PasswordEnv)
		if er != nil {
			return nil, local("setup", "proxy_secret_missing")
		}
		switch p.Kind {
		case "http-connect", "https-connect":
			conn, e = connect(conn, dest, p.Username, pass)
		case "socks5":
			e = socks(conn, dest, p.Username, pass)
		default:
			return nil, local("setup", "unsupported_proxy_hop")
		}
		if e != nil {
			return nil, e
		}
	}
	if e = ctx.Err(); e != nil {
		return nil, fail("connect", e)
	}
	ok = true
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func connect(conn net.Conn, address, user, pass string) (net.Conn, error) {
	if strings.ContainsAny(address, "\r\n") {
		return conn, local("setup", "invalid_connect_target")
	}
	req := "CONNECT " + address + " HTTP/1.1\r\nHost: " + address + "\r\n"
	if user != "" || pass != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)) + "\r\n"
	}
	req += "\r\n"
	if _, e := io.WriteString(conn, req); e != nil {
		return conn, fail("proxy_connect", e)
	}
	r := bufio.NewReader(conn)
	header := make([]byte, 0, 512)
	for len(header) < 32768 {
		b, e := r.ReadByte()
		if e != nil {
			return conn, fail("proxy_connect", e)
		}
		header = append(header, b)
		if bytes.HasSuffix(header, []byte("\r\n\r\n")) {
			break
		}
	}
	if !bytes.HasSuffix(header, []byte("\r\n\r\n")) {
		return conn, &failure{stage: "proxy_connect", code: "header_too_large"}
	}
	resp, e := http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), &http.Request{Method: "CONNECT"})
	if e != nil {
		return conn, &failure{stage: "proxy_connect", code: "invalid_response"}
	}
	if resp.StatusCode != 200 {
		return conn, &failure{stage: "proxy_connect", code: fmt.Sprintf("http_%d", resp.StatusCode)}
	}
	return &bufferedConn{Conn: conn, r: r}, nil
}
func socks(conn net.Conn, address, user, pass string) error {
	method := byte(0)
	if user != "" || pass != "" {
		method = 2
	}
	if _, e := conn.Write([]byte{5, 1, method}); e != nil {
		return fail("proxy_socks", e)
	}
	var b [2]byte
	if _, e := io.ReadFull(conn, b[:]); e != nil {
		return fail("proxy_socks", e)
	}
	if b[0] != 5 || b[1] != method {
		return &failure{stage: "proxy_socks", code: "authentication_method_rejected"}
	}
	if method == 2 {
		if len(user) > 255 || len(pass) > 255 {
			return local("setup", "proxy_credentials_too_long")
		}
		auth := []byte{1, byte(len(user))}
		auth = append(auth, []byte(user)...)
		auth = append(auth, byte(len(pass)))
		auth = append(auth, []byte(pass)...)
		if _, e := conn.Write(auth); e != nil {
			return fail("proxy_socks", e)
		}
		if _, e := io.ReadFull(conn, b[:]); e != nil {
			return fail("proxy_socks", e)
		}
		if b[0] != 1 || b[1] != 0 {
			return &failure{stage: "proxy_socks", code: "authentication_rejected"}
		}
	}
	payload, e := socksAddress(address)
	if e != nil {
		return e
	}
	req := append([]byte{5, 1, 0}, payload...)
	if _, e = conn.Write(req); e != nil {
		return fail("proxy_socks", e)
	}
	var h [4]byte
	if _, e = io.ReadFull(conn, h[:]); e != nil {
		return fail("proxy_socks", e)
	}
	if h[0] != 5 || h[2] != 0 || h[1] != 0 {
		return &failure{stage: "proxy_socks", code: fmt.Sprintf("reply_%d", h[1])}
	}
	n := 0
	switch h[3] {
	case 1:
		n = 4
	case 4:
		n = 16
	case 3:
		var l [1]byte
		if _, e = io.ReadFull(conn, l[:]); e != nil {
			return fail("proxy_socks", e)
		}
		n = int(l[0])
	default:
		return &failure{stage: "proxy_socks", code: "invalid_address_type"}
	}
	_, e = io.ReadFull(conn, make([]byte, n+2))
	if e != nil {
		return fail("proxy_socks", e)
	}
	return nil
}
func socksAddress(address string) ([]byte, error) {
	h, p, e := net.SplitHostPort(address)
	if e != nil {
		return nil, local("setup", "invalid_socks_target")
	}
	n, e := strconv.Atoi(p)
	if e != nil || n < 1 || n > 65535 {
		return nil, local("setup", "invalid_socks_port")
	}
	var b []byte
	if ip := net.ParseIP(h); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			b = append([]byte{1}, ip4...)
		} else {
			b = append([]byte{4}, ip.To16()...)
		}
	} else {
		if len(h) < 1 || len(h) > 255 {
			return nil, local("setup", "invalid_socks_hostname")
		}
		b = append([]byte{3, byte(len(h))}, []byte(h)...)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(n))
	return b, nil
}
func sshDial(ctx context.Context, p model.Proxy, target, bindIP string) (net.Conn, error) {
	for _, f := range []string{p.SSHIdentityFile, p.SSHKnownHosts} {
		s, e := os.Stat(f)
		if e != nil || !s.Mode().IsRegular() {
			return nil, local("setup", "ssh_file_unreadable")
		}
	}
	identity, e := sshFile(p.SSHIdentityFile)
	if e != nil {
		return nil, e
	}
	known, e := sshFile(p.SSHKnownHosts)
	if e != nil {
		return nil, e
	}
	bin, e := exec.LookPath("ssh")
	if e != nil {
		return nil, local("setup", "ssh_not_installed")
	}
	host, port, e := net.SplitHostPort(p.Address)
	if e != nil || strings.HasPrefix(host, "-") {
		return nil, local("setup", "invalid_ssh_host")
	}
	// Explicit options avoid ambient ssh config, proxy commands, agent forwarding,
	// password prompts, multiplexing and host-key acceptance.
	args := []string{"-F", os.DevNull, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "ForwardAgent=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "UserKnownHostsFile=\"" + known + "\"", "-o", "GlobalKnownHostsFile=" + os.DevNull, "-i", identity, "-p", port, "-l", p.SSHUser, "-W", target, host}
	if bindIP != "" {
		args = append(args[:len(args)-1], "-b", bindIP, host)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, local("setup", "ssh_pipe_failed")
	}
	started := false
	defer func() {
		if !started {
			in.Close()
		}
	}()
	out, e := cmd.StdoutPipe()
	if e != nil {
		return nil, local("setup", "ssh_pipe_failed")
	}
	defer func() {
		if !started {
			out.Close()
		}
	}()
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		return nil, local("setup", "ssh_start_failed")
	}
	started = true
	a, b := net.Pipe()
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			in.Close()
			b.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
	}
	go func() { _, _ = io.Copy(in, b); closeAll() }()
	go func() { _, _ = io.Copy(b, out); closeAll(); _ = cmd.Wait() }()
	return a, nil
}

func sshFile(path string) (string, error) {
	path, e := filepath.Abs(path)
	if e != nil {
		return "", local("setup", "invalid_ssh_file")
	}
	path = filepath.ToSlash(path)
	// OpenSSH config quoting is not Go string quoting. Avoid expansion of
	// environment variables and syntax that cannot name one literal file.
	if strings.ContainsAny(path, "\"\\\r\n\x00") || strings.Contains(path, "${") {
		return "", local("setup", "invalid_ssh_file")
	}
	return strings.ReplaceAll(path, "%", "%%"), nil
}
func classify(err error) (outcome, stage, code string) {
	var f *failure
	if errors.As(err, &f) {
		o := "fail"
		if f.local {
			o = "error"
		}
		return o, f.stage, f.code
	}
	return "fail", "connect", secure.ErrorCode(err)
}
