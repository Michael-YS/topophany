package api

import (
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

const (
	// DefaultIngressWindow is how long a handshake failure is remembered.
	// Delivery retries are minutes apart, so a short window would miss a
	// burst that has just stopped.
	DefaultIngressWindow = 30 * time.Minute
	// maxIngressSources bounds memory: a scanner can present many addresses.
	maxIngressSources = 256
	// shownIngressSources bounds what a snapshot shows.
	shownIngressSources = 8
	// tlsHandshakePrefix is net/http's own wording for a failed handshake
	// on the HTTP server's connection. Only lines with it count.
	tlsHandshakePrefix = "http: TLS handshake error from "
)

type ingressSource struct {
	count int
	last  time.Time
}

// IngressCounter counts TLS handshake failures by source over a rolling
// window. It observes the HTTP server's error log rather than the sockets:
// net/http does the handshake, and its error line is the only place the
// failure is reported. A failed handshake is evidence that an observer's
// delivery did not arrive; it is not evidence about anything the observer
// watches, and it cannot be attributed to an observer because the observer
// identifies itself only after the handshake succeeds.
type IngressCounter struct {
	mu      sync.Mutex
	window  time.Duration
	now     func() time.Time
	sources map[string]*ingressSource
}

func NewIngressCounter(window time.Duration) *IngressCounter {
	if window <= 0 {
		window = DefaultIngressWindow
	}
	return &IngressCounter{window: window, now: time.Now, sources: map[string]*ingressSource{}}
}

// Writer passes every byte to next and counts the handshake failures among
// them. Give it to the HTTP server's ErrorLog: log.Logger writes one message
// per call, so a message is one line.
func (c *IngressCounter) Writer(next func(string)) *ingressWriter {
	return &ingressWriter{counter: c, next: next}
}

type ingressWriter struct {
	counter *IngressCounter
	next    func(string)
}

func (w *ingressWriter) Write(p []byte) (int, error) {
	line := strings.TrimSuffix(string(p), "\n")
	if w.next != nil {
		w.next(line)
	}
	w.counter.observe(line)
	return len(p), nil
}

// observe counts line when it is the HTTP server reporting a failed TLS
// handshake, and ignores every other line. The line arrives as log.Logger
// formatted it, so a timestamp can come first.
func (c *IngressCounter) observe(line string) {
	at := strings.Index(line, tlsHandshakePrefix)
	if at < 0 {
		return
	}
	rest := line[at+len(tlsHandshakePrefix):]
	// "<host>:<port>: <error>"; the address is everything before the first
	// ": " after the port, and IPv6 addresses keep their brackets.
	cut := strings.Index(rest, ": ")
	if cut < 0 {
		return
	}
	host, port, err := net.SplitHostPort(rest[:cut])
	if err != nil || host == "" {
		return
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	s := c.sources[host]
	if s == nil {
		if len(c.sources) >= maxIngressSources {
			c.evictOldest()
		}
		s = &ingressSource{}
		c.sources[host] = s
	}
	s.count++
	s.last = now
}

// prune drops sources whose last failure fell out of the window. Callers
// hold c.mu.
func (c *IngressCounter) prune(now time.Time) {
	for host, s := range c.sources {
		if now.Sub(s.last) > c.window {
			delete(c.sources, host)
		}
	}
}

// evictOldest makes room for a new address. Callers hold c.mu.
func (c *IngressCounter) evictOldest() {
	oldest := ""
	var at time.Time
	for host, s := range c.sources {
		if oldest == "" || s.last.Before(at) {
			oldest, at = host, s.last
		}
	}
	delete(c.sources, oldest)
}

// Summary is the window as a snapshot shows it, or nil when nothing failed
// inside it. Sources are the busiest first, bounded.
func (c *IngressCounter) Summary(now time.Time) *model.Ingress {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if len(c.sources) == 0 {
		return nil
	}
	out := &model.Ingress{WindowSeconds: int(c.window / time.Second)}
	for host, s := range c.sources {
		out.Failures += s.count
		if s.last.After(out.Last) {
			out.Last = s.last
		}
		out.Sources = append(out.Sources, model.IngressSource{Address: host, Count: s.count, Last: s.last})
	}
	slices.SortFunc(out.Sources, func(a, b model.IngressSource) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Address, b.Address)
	})
	if len(out.Sources) > shownIngressSources {
		out.Sources = out.Sources[:shownIngressSources]
	}
	return out
}
