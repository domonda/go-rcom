package rcom

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/domonda/golog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ExecuteRemotely_errors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}

	server := httptest.NewServer(&service{allowedCMDs: map[string]bool{"sh": true}})
	defer server.Close()

	t.Run("command not allowed by server", func(t *testing.T) {
		_, err := ExecuteRemotely(context.Background(), server.URL, &Command{Name: "echo", Args: []string{"not executed"}})
		assert.ErrorContains(t, err, "400 Bad Request")
		assert.ErrorContains(t, err, `command "echo" not allowed`)
	})

	t.Run("command fails on server", func(t *testing.T) {
		command := &Command{Name: "sh", Args: []string{"-c", "echo server-side failure; exit 1"}}
		_, err := ExecuteRemotely(context.Background(), server.URL, command)
		assert.ErrorContains(t, err, "500 Internal Server Error")
		assert.ErrorContains(t, err, "server-side failure", "error contains the command output")
	})

	t.Run("invalid command is not sent", func(t *testing.T) {
		_, err := ExecuteRemotely(context.Background(), server.URL, &Command{})
		assert.ErrorContains(t, err, "no command name")
	})

	t.Run("response body is closed", func(t *testing.T) {
		// Every closed error response returns its connection to the idle pool,
		// so repeated failing calls reuse one connection
		var newConns atomic.Int32
		server := httptest.NewUnstartedServer(&service{})
		server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				newConns.Add(1)
			}
		}
		server.Start()
		defer server.Close()

		for range 10 {
			_, err := ExecuteRemotely(context.Background(), server.URL, &Command{Name: "echo"})
			require.Error(t, err)
		}
		assert.Equal(t, int32(1), newConns.Load(), "connections opened")
	})
}

func Test_encodeCommand(t *testing.T) {
	command, _ := cpCommand()
	for range 2000 {
		command.Args = append(command.Args, strings.Repeat("a", 50))
	}
	for i := range 8 {
		command.Files[fmt.Sprintf("%d.bin", i)] = make([]byte, 256<<10)
	}
	command.Stdin = []byte("stdin")
	body, err := encodeCommand(command)
	require.NoError(t, err)
	// Sized up front instead of grown, which copies
	// the data and leaves up to twice its size allocated
	assert.Less(t, cap(body), len(body)*11/10, "capacity %d for %d bytes", cap(body), len(body))

	decoded, err := readWireCommand(bufio.NewReader(bytes.NewReader(body)))
	require.NoError(t, err)
	assert.Equal(t, command.Files, decoded.Files)
}

func Test_ExecuteRemotely_requestIDOfLoadBalancerError(t *testing.T) {
	// A load balancer responds without the request ID
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no server available", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	command, _ := cpCommand()
	_, err := ExecuteRemotely(golog.ContextWithRequestID(context.Background(), "job-42"), server.URL, command)
	responseErr, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok, "error %v is a ResponseError", err)
	assert.Equal(t, "job-42", responseErr.RequestID, "the sent request ID")
}
