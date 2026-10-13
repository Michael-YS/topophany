package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func dns(ctx context.Context, x model.Check, target string, o *model.Observation) error {
	target = pinned(target, x.ResolveIP)
	var idb [2]byte
	if _, e := rand.Read(idb[:]); e != nil {
		return local("setup", "entropy_failure")
	}
	id := binary.BigEndian.Uint16(idb[:])
	qt := uint16(1)
	if x.DNS.Type == "AAAA" {
		qt = 28
	}
	q, e := dnsQuery(id, x.DNS.Name, qt)
	if e != nil {
		return local("setup", "invalid_dns_question")
	}
	nd := net.Dialer{}
	if x.BindIP != "" {
		nd.LocalAddr = &net.UDPAddr{IP: net.ParseIP(x.BindIP)}
	}
	conn, e := nd.DialContext(ctx, "udp", target)
	if e != nil {
		return fail("dns_transport", e)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if _, e = conn.Write(q); e != nil {
		return fail("dns_transport", e)
	}
	buf := make([]byte, 65535)
	n, e := conn.Read(buf)
	if e != nil {
		return fail("dns_transport", e)
	}
	b := buf[:n]
	o.Facts["dns_transport"] = "udp"
	if len(b) >= 4 && binary.BigEndian.Uint16(b) == id && b[2]&2 != 0 {
		td := net.Dialer{}
		if x.BindIP != "" {
			td.LocalAddr = &net.TCPAddr{IP: net.ParseIP(x.BindIP)}
		}
		tcp, e := td.DialContext(ctx, "tcp", target)
		if e != nil {
			return fail("dns_tcp", e)
		}
		defer tcp.Close()
		if dl, ok := ctx.Deadline(); ok {
			_ = tcp.SetDeadline(dl)
		}
		stop := context.AfterFunc(ctx, func() { tcp.Close() })
		defer stop()
		packet := binary.BigEndian.AppendUint16(nil, uint16(len(q)))
		packet = append(packet, q...)
		if _, e = tcp.Write(packet); e != nil {
			return fail("dns_tcp", e)
		}
		var lb [2]byte
		if _, e = io.ReadFull(tcp, lb[:]); e != nil {
			return fail("dns_tcp", e)
		}
		b = make([]byte, int(binary.BigEndian.Uint16(lb[:])))
		if _, e = io.ReadFull(tcp, b); e != nil {
			return fail("dns_tcp", e)
		}
		o.Facts["dns_transport"] = "tcp_fallback"
	}
	ips, e := parseDNS(b, id, x.DNS.Name, qt)
	if e != nil {
		return &failure{stage: "dns_answer", code: "invalid_or_negative_answer"}
	}
	if len(ips) == 0 {
		return &failure{stage: "dns_answer", code: "no_address_answer"}
	}
	if len(x.DNS.ExpectedIPs) > 0 {
		allow := map[netip.Addr]bool{}
		for _, s := range x.DNS.ExpectedIPs {
			ip, _ := netip.ParseAddr(s)
			allow[ip.Unmap()] = true
		}
		for _, ip := range ips {
			if !allow[ip.Unmap()] {
				return &failure{stage: "dns_answer", code: "address_outside_allowlist"}
			}
		}
	}
	vals := make([]string, 0, len(ips))
	for _, ip := range ips {
		vals = append(vals, ip.String())
	}
	addresses := strings.Join(vals, ",")
	if len(addresses) > 2048 {
		return local("dns_answer", "address_evidence_budget_exceeded")
	}
	o.Facts["addresses"] = addresses
	o.Facts["response_proof"] = "dns_response_not_dnssec"
	o.Outcome = "ok"
	o.Stage = "dns_answer"
	o.Connectivity = true
	o.Detail = "configured resolver returned addresses satisfying assertions; DNSSEC not validated"
	return nil
}
func dnsQuery(id uint16, name string, qt uint16) ([]byte, error) {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b, id)
	binary.BigEndian.PutUint16(b[2:], 0x0100)
	binary.BigEndian.PutUint16(b[4:], 1)
	name = strings.TrimSuffix(name, ".")
	if len(name) > 253 {
		return nil, errors.New("name too long")
	}
	for _, p := range strings.Split(name, ".") {
		if len(p) == 0 || len(p) > 63 {
			return nil, errors.New("invalid label")
		}
		b = append(b, byte(len(p)))
		b = append(b, p...)
	}
	b = append(b, 0)
	b = binary.BigEndian.AppendUint16(b, qt)
	b = binary.BigEndian.AppendUint16(b, 1)
	return b, nil
}

// dnsName bounds pointers, labels and expanded name length; a malformed or
// cyclic compression pointer must never hang an executor.
func dnsName(b []byte, off int) (string, int, error) {
	next := -1
	seen := map[int]bool{}
	parts := []string{}
	size := 0
	for steps := 0; steps < 128; steps++ {
		if off < 0 || off >= len(b) || seen[off] {
			return "", 0, errors.New("invalid DNS name pointer")
		}
		seen[off] = true
		l := int(b[off])
		if l&0xc0 == 0xc0 {
			if off+1 >= len(b) {
				return "", 0, io.ErrUnexpectedEOF
			}
			if next < 0 {
				next = off + 2
			}
			off = (l&0x3f)<<8 | int(b[off+1])
			continue
		}
		off++
		if l == 0 {
			if next < 0 {
				next = off
			}
			return strings.ToLower(strings.Join(parts, ".")), next, nil
		}
		if l > 63 || off+l > len(b) {
			return "", 0, errors.New("invalid DNS label")
		}
		size += l + 1
		if size > 254 {
			return "", 0, errors.New("expanded DNS name too long")
		}
		// A literal dot inside a wire label is not a label separator. Reject
		// ambiguous presentation names instead of matching an unrelated owner.
		label := b[off : off+l]
		for _, c := range label {
			if c <= 32 || c >= 127 || c == '.' || c == '\\' {
				return "", 0, errors.New("unsupported DNS label character")
			}
		}
		parts = append(parts, string(label))
		off += l
	}
	return "", 0, errors.New("DNS pointer budget exceeded")
}
func parseDNS(b []byte, id uint16, name string, qt uint16) ([]netip.Addr, error) {
	if len(b) < 12 || binary.BigEndian.Uint16(b) != id || b[2]&0x80 == 0 || b[2]&0x78 != 0 || b[2]&2 != 0 || b[3]&15 != 0 || binary.BigEndian.Uint16(b[4:]) != 1 {
		return nil, errors.New("bad DNS header")
	}
	q, off, e := dnsName(b, 12)
	if e != nil {
		return nil, e
	}
	if off+4 > len(b) || q != strings.ToLower(strings.TrimSuffix(name, ".")) || binary.BigEndian.Uint16(b[off:]) != qt || binary.BigEndian.Uint16(b[off+2:]) != 1 {
		return nil, errors.New("mismatched DNS question")
	}
	off += 4
	an := int(binary.BigEndian.Uint16(b[6:]))
	if an > 1024 {
		return nil, errors.New("too many DNS answers")
	}
	byName := map[string][]netip.Addr{}
	cname := map[string]string{}
	for i := 0; i < an; i++ {
		owner, n, e := dnsName(b, off)
		if e != nil {
			return nil, e
		}
		off = n
		if off+10 > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		typ := binary.BigEndian.Uint16(b[off:])
		class := binary.BigEndian.Uint16(b[off+2:])
		ln := int(binary.BigEndian.Uint16(b[off+8:]))
		off += 10
		if off+ln > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		if class == 1 {
			if typ == qt && (typ == 1 && ln == 4 || typ == 28 && ln == 16) {
				ip, ok := netip.AddrFromSlice(b[off : off+ln])
				if ok {
					byName[owner] = append(byName[owner], ip)
				}
			}
			if typ == 5 {
				v, end, e := dnsName(b, off)
				if e != nil || end != off+ln {
					return nil, errors.New("invalid CNAME")
				}
				cname[owner] = v
			}
		}
		off += ln
	}
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		if seen[q] {
			return nil, errors.New("CNAME cycle")
		}
		seen[q] = true
		if ips := byName[q]; len(ips) > 0 {
			return ips, nil
		}
		n, ok := cname[q]
		if !ok {
			return nil, nil
		}
		q = n
	}
	return nil, errors.New("CNAME budget exceeded")
}
func udpEcho(ctx context.Context, x model.Check, target string, o *model.Observation) error {
	target = pinned(target, x.ResolveIP)
	d := net.Dialer{}
	if x.BindIP != "" {
		d.LocalAddr = &net.UDPAddr{IP: net.ParseIP(x.BindIP)}
	}
	c, e := d.DialContext(ctx, "udp", target)
	if e != nil {
		return fail("udp", e)
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	payload := make([]byte, 32)
	copy(payload, []byte("TOPH"))
	if _, e = rand.Read(payload[4:]); e != nil {
		return local("setup", "entropy_failure")
	}
	if _, e = c.Write(payload); e != nil {
		return fail("udp", e)
	}
	buf := make([]byte, 1024)
	n, e := c.Read(buf)
	if e != nil {
		return fail("udp", e)
	}
	if !bytes.Equal(buf[:n], payload) {
		return &failure{stage: "udp_echo", code: "payload_mismatch"}
	}
	o.Outcome = "ok"
	o.Connectivity = true
	o.Stage = "udp_echo"
	o.Facts["response_proof"] = "nonce_echo"
	o.Detail = "nonce echoed by the configured UDP endpoint; does not test arbitrary UDP services"
	return nil
}
func checksum(b []byte) uint16 {
	sum := uint32(0)
	for len(b) > 1 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 > 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}
func icmp(ctx context.Context, x model.Check, target string, o *model.Observation, listen func(string, string) (net.PacketConn, error)) error {
	if listen == nil {
		listen = net.ListenPacket
	}
	bind := x.BindIP
	if bind == "" {
		bind = "0.0.0.0"
	}
	c, e := listen("ip4:icmp", bind)
	if e != nil {
		if errors.Is(e, os.ErrPermission) {
			return local("icmp", "raw_socket_permission_required")
		}
		return local("icmp", "raw_socket_unavailable")
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	packet := make([]byte, 32)
	if _, e = rand.Read(packet[4:]); e != nil {
		return local("setup", "entropy_failure")
	}
	packet[0] = 8
	binary.BigEndian.PutUint16(packet[2:], checksum(packet))
	start := time.Now()
	if _, e = c.WriteTo(packet, &net.IPAddr{IP: net.ParseIP(target)}); e != nil {
		return fail("icmp", e)
	}
	buf := make([]byte, 1500)
	for i := 0; i < 128; i++ {
		n, src, e := c.ReadFrom(buf)
		if e != nil {
			return fail("icmp", e)
		}
		ip, ok := src.(*net.IPAddr)
		if !ok || !ip.IP.Equal(net.ParseIP(target)) {
			continue
		}
		b := buf[:n]
		if len(b) > 0 && b[0]>>4 == 4 {
			h := int(b[0]&15) * 4
			if h < 20 || h > len(b) {
				continue
			}
			b = b[h:]
		}
		if len(b) != len(packet) || b[0] != 0 || b[1] != 0 || checksum(b) != 0 || !bytes.Equal(b[4:], packet[4:]) {
			continue
		}
		o.Outcome = "ok"
		o.Connectivity = true
		o.Stage = "icmp"
		o.Facts["response_proof"] = "icmp_echo"
		o.Facts["rtt_ms"] = strconv.FormatInt(time.Since(start).Milliseconds(), 10)
		o.Detail = "IPv4 ICMP echo reply; TCP, UDP and services not inferred"
		return nil
	}
	return &failure{stage: "icmp", code: "reply_budget_exceeded"}
}
