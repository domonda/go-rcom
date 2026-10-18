//go:build unix

package rcom

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startObservedServer starts a test server for svc and returns its address
// and a channel that is closed when the service has handled a request
func startObservedServer(t *testing.T, svc *service) (addr string, finished chan struct{}) {
	t.Helper()
	finished = make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		svc.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://"), finished
}

// sendRawRequest writes an HTTP request for command to a new connection
// and returns the connection without reading the response
func sendRawRequest(t *testing.T, addr string, command *Command) net.Conn {
	t.Helper()
	body := bytes.NewBuffer(nil)
	require.NoError(t, gob.NewEncoder(body).Encode(newWireCommand(command)))
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: rcom\r\nContent-Length: %d\r\n\r\n%s", body.Len(), body.Bytes())
	require.NoError(t, err)
	return conn
}

func requireFinished(t *testing.T, finished chan struct{}, msg string) {
	t.Helper()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal(msg)
	}
}

func Test_service_clientDisconnects(t *testing.T) {
	for name, extraData := range map[string]string{
		"after the request":            "",
		"after sending more than that": "X",
	} {
		t.Run(name, func(t *testing.T) {
			svc := &service{allowedCMDs: map[string]bool{"sleep": true}}
			addr, finished := startObservedServer(t, svc)

			conn := sendRawRequest(t, addr, &Command{Name: "sleep", Args: []string{"30"}})
			time.Sleep(100 * time.Millisecond) // Let the command start
			_, err := conn.Write([]byte(extraData))
			require.NoError(t, err)
			require.NoError(t, conn.Close())

			requireFinished(t, finished, "command still running after the client disconnected")
		})
	}
}

func Test_service_MaxCommandDuration(t *testing.T) {
	svc := &service{allowedCMDs: map[string]bool{"sleep": true}, maxCommandDuration: 50 * time.Millisecond}
	addr, _ := startObservedServer(t, svc)

	conn := sendRawRequest(t, addr, &Command{Name: "sleep", Args: []string{"10"}})
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	message, _ := io.ReadAll(response.Body)
	assert.Equal(t, http.StatusInternalServerError, response.StatusCode)
	assert.Contains(t, string(message), "deadline exceeded")
}

func Test_service_StallTimeout(t *testing.T) {
	t.Run("client stops sending", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{"sleep": true}, stallTimeout: 100 * time.Millisecond}
		addr, finished := startObservedServer(t, svc)

		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()
		_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: rcom\r\nContent-Length: 1000\r\n\r\npartial")
		require.NoError(t, err)

		requireFinished(t, finished, "still reading a stalled request")
	})

	t.Run("client stops receiving", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{"head": true}, stallTimeout: 100 * time.Millisecond}
		addr, finished := startObservedServer(t, svc)

		// The result is larger than the socket buffers
		// and the client never reads the response
		command := &Command{Name: "head", Args: []string{"-c", "100000000", "/dev/zero"}}
		setVar(t, &MaxOutputSize, 0)
		sendRawRequest(t, addr, command)

		requireFinished(t, finished, "still writing to a client that stopped receiving")
	})
}

func Test_service_hijackedResponse(t *testing.T) {
	svc := &service{allowedCMDs: map[string]bool{"echo": true}}
	addr, _ := startObservedServer(t, svc)

	conn := sendRawRequest(t, addr, &Command{Name: "echo", Args: []string{"hello"}})
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.True(t, response.Close, "Connection: close")

	var wire wireResult
	require.NoError(t, gob.NewDecoder(response.Body).Decode(&wire))
	assert.Equal(t, "hello\n", wire.Stdout)
}
