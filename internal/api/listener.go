package api

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

const maxConnectionLifetime = 2 * time.Minute

// LimitListener bounds connections before TLS or HTTP parsing. Each source gets
// at most a quarter of the global capacity (and never more than 16 slots).
// IPv4-mapped addresses share a quota with IPv4; IPv6 addresses share a /64.
// Source identity comes only from the socket, never from forwarded HTTP headers.
func LimitListener(inner net.Listener, maximum int) net.Listener {
	if maximum < 1 {
		panic("invalid connection limit")
	}
	return newLimitedListener(inner, maximum, max(1, min(16, maximum/4)), maxConnectionLifetime)
}

func newLimitedListener(inner net.Listener, maximum, perSource int, lifetime time.Duration) *limitedListener {
	if maximum < 1 || perSource < 1 || perSource > maximum || lifetime <= 0 {
		panic("invalid connection limits")
	}
	return &limitedListener{Listener: inner, slots: make(chan struct{}, maximum),
		perSource: perSource, lifetime: lifetime, sources: make(map[string]int)}
}

type limitedListener struct {
	net.Listener
	slots     chan struct{}
	mu        sync.Mutex
	perSource int
	lifetime  time.Duration
	sources   map[string]int
}

func connectionSource(a net.Addr) string {
	if a == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return "unknown"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	ip = ip.WithZone("").Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String()
	}
	return ip.String()
}

func (l *limitedListener) reserve(source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sources[source] >= l.perSource {
		return false
	}
	select {
	case l.slots <- struct{}{}:
		l.sources[source]++
		return true
	default:
		return false
	}
}

func (l *limitedListener) release(source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	<-l.slots
	if l.sources[source]--; l.sources[source] == 0 {
		delete(l.sources, source)
	}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		source := connectionSource(c.RemoteAddr())
		if !l.reserve(source) {
			_ = c.Close()
			continue
		}
		out := &limitedConn{Conn: c, release: func() { l.release(source) }}
		// A timer, rather than a socket deadline, also bounds active keep-alive
		// and HTTP/2 connections: net/http cannot extend it with SetDeadline.
		out.mu.Lock()
		out.timer = time.AfterFunc(l.lifetime, func() { _ = out.Close() })
		out.mu.Unlock()
		return out, nil
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	mu      sync.Mutex
	timer   *time.Timer
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.mu.Lock()
		if c.timer != nil {
			c.timer.Stop()
		}
		c.mu.Unlock()
		c.release()
	})
	return err
}
