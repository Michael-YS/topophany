package api

import (
	"context"
	"net"
	"time"
)

type connectionKey struct{}

// ConnectionContext associates a socket with requests, including HTTP/2
// streams. ServeTLS wraps the limited socket in a tls.Conn (NetConn).
func ConnectionContext(ctx context.Context, c net.Conn) context.Context {
	if wrapped, ok := c.(interface{ NetConn() net.Conn }); ok {
		c = wrapped.NetConn()
	}
	if limited, ok := c.(*limitedConn); ok {
		return context.WithValue(ctx, connectionKey{}, limited)
	}
	return ctx
}

func authenticateConnection(ctx context.Context, principal string) bool {
	c, ok := ctx.Value(connectionKey{}).(*limitedConn)
	return !ok || c.authenticate(principal)
}

func (c *limitedConn) authenticate(principal string) bool {
	l := c.listener
	l.mu.Lock()
	defer l.mu.Unlock()
	if c.closed {
		return false
	}
	if c.principal != "" {
		// Charge the socket to its first principal; every request still
		// authenticates and has its own principal's request budgets.
		return true
	}
	maximum := cap(l.slots)
	// Leave a quarter of all sockets available for authentication, even if
	// authenticated peers keep their connections open. Bound each principal.
	if l.authenticated >= max(1, maximum-max(1, maximum/4)) || l.principals[principal] >= max(1, min(8, maximum/4)) {
		return false
	}
	l.removePending(c)
	c.principal = principal
	l.principals[principal]++
	l.authenticated++
	c.mu.Lock()
	c.timer.Reset(max(0, time.Until(c.accepted.Add(l.lifetime))))
	c.mu.Unlock()
	return true
}
