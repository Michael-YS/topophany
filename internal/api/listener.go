package api

import (
	"net"
	"sync"
)

// LimitListener bounds accepted connections before TLS or HTTP parsing. Excess
// connections are closed immediately; header/idle deadlines release stalled slots.
func LimitListener(inner net.Listener, maximum int) net.Listener {
	if maximum < 1 {
		panic("invalid connection limit")
	}
	return &limitedListener{Listener: inner, slots: make(chan struct{}, maximum)}
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			c.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
