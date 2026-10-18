package rcom

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_limitListener(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listener := newLimitListener(tcpListener, 1)
	defer listener.Close()

	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- conn
		}
	}()
	for range 2 {
		conn, err := net.Dial("tcp", tcpListener.Addr().String())
		require.NoError(t, err)
		defer conn.Close()
	}

	first := <-accepted
	select {
	case <-accepted:
		t.Fatal("second connection accepted while the first is open")
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, first.Close())
	_ = first.Close() // Releases the slot only once
	second := <-accepted
	require.NotNil(t, second)
	second.Close()

	// Close ends an Accept waiting for a free slot
	conn, err := net.Dial("tcp", tcpListener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	third := <-accepted
	require.NotNil(t, third)
	require.NoError(t, listener.Close())
	_, open := <-accepted
	assert.False(t, open, "Accept returns after Close")
	third.Close()
}

func Test_newLimitListener_noLimit(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer tcpListener.Close()
	assert.Equal(t, tcpListener, newLimitListener(tcpListener, 0))
}

func Test_limitListenerConn_CloseWrite(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listener := newLimitListener(tcpListener, 1)
	defer listener.Close()

	client, err := net.Dial("tcp", tcpListener.Addr().String())
	require.NoError(t, err)
	defer client.Close()
	conn, err := listener.Accept()
	require.NoError(t, err)
	defer conn.Close()

	// net/http half-closes connections through this interface
	closeWriter, ok := conn.(interface{ CloseWrite() error })
	require.True(t, ok, "CloseWrite of the TCP connection is available")
	require.NoError(t, closeWriter.CloseWrite())
	_, err = client.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}
