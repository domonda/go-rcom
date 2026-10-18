package rcom

import (
	"net"
	"sync"
)

// limitListener accepts at most a fixed number of connections at the same
// time. Accept waits until an accepted connection was closed.
type limitListener struct {
	net.Listener
	slots     chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

// newLimitListener returns listener unchanged if maxConns is zero or negative
func newLimitListener(listener net.Listener, maxConns int) net.Listener {
	if maxConns <= 0 {
		return listener
	}
	return &limitListener{
		Listener: listener,
		slots:    make(chan struct{}, maxConns),
		closed:   make(chan struct{}),
	}
}

func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.closed:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitListenerConn{Conn: conn, release: func() { <-l.slots }}, nil
}

func (l *limitListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

type limitListenerConn struct {
	net.Conn
	releaseOnce sync.Once
	release     func()
}

func (c *limitListenerConn) Close() error {
	err := c.Conn.Close()
	c.releaseOnce.Do(c.release)
	return err
}
