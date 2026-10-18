package rcom

import (
	"bytes"
	"context"
	"encoding/gob"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startSignaledServer serves an rcom service allowing "sleep" until a signal
// is sent to the returned channel. The started channel is closed when a
// request reaches the service, finished when the service has handled it.
func startSignaledServer(t *testing.T, timeout time.Duration) (addr string, signals chan os.Signal, started, finished chan struct{}, served chan error) {
	t.Helper()

	started = make(chan struct{})
	finished = make(chan struct{})
	svc := &service{allowedCMDs: map[string]bool{"sleep": true}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		svc.ServeHTTP(w, r)
		close(finished)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	signals = make(chan os.Signal, 1)
	served = make(chan error, 1)
	go func() { served <- serveUntilSignal(&http.Server{Handler: handler}, listener, signals, timeout) }()

	return "http://" + listener.Addr().String(), signals, started, finished, served
}

func Test_serveUntilSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}

	t.Run("waits for running requests", func(t *testing.T) {
		addr, signals, started, finished, served := startSignaledServer(t, time.Minute)

		requestErr := make(chan error, 1)
		go func() {
			_, err := ExecuteRemotely(context.Background(), addr, &Command{Name: "sleep", Args: []string{"0.2"}})
			requestErr <- err
		}()
		<-started
		signals <- syscall.SIGTERM

		assert.ErrorIs(t, <-served, http.ErrServerClosed)
		select {
		case <-finished:
		default:
			t.Error("returned before the running request was finished")
		}
		assert.NoError(t, <-requestErr)
	})

	t.Run("cancels requests after the timeout", func(t *testing.T) {
		addr, signals, started, _, served := startSignaledServer(t, time.Millisecond)

		requestErr := make(chan error, 1)
		go func() {
			_, err := ExecuteRemotely(context.Background(), addr, &Command{Name: "sleep", Args: []string{"10"}})
			requestErr <- err
		}()
		<-started
		signals <- syscall.SIGTERM

		assert.ErrorIs(t, <-served, context.DeadlineExceeded)
		assert.Error(t, <-requestErr, "canceled request")
	})
}

func Test_ListenAndServe_portInUse(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer listener.Close()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)

	assert.Error(t, ListenAndServe(port, true, "echo"), "graceful")
	assert.Error(t, ListenAndServe(port, false, "echo"), "not graceful")
}

func Test_newService(t *testing.T) {
	defaultMaxRequestSize, defaultMaxConcurrentCommands := MaxRequestSize, MaxConcurrentCommands
	t.Cleanup(func() { MaxRequestSize, MaxConcurrentCommands = defaultMaxRequestSize, defaultMaxConcurrentCommands })

	MaxRequestSize, MaxConcurrentCommands = 1000, 2
	svc := newService([]string{"convert", "ffmpeg"})
	assert.Equal(t, map[string]bool{"convert": true, "ffmpeg": true}, svc.allowedCMDs)
	assert.Equal(t, int64(1000), svc.maxRequestSize)
	assert.Equal(t, 2, cap(svc.running))

	MaxConcurrentCommands = 0
	assert.Nil(t, newService(nil).running, "no concurrency limit")
}

func Test_service_limits(t *testing.T) {
	encodeRequest := func(t *testing.T, command *Command) *bytes.Buffer {
		t.Helper()
		body := bytes.NewBuffer(nil)
		require.NoError(t, gob.NewEncoder(body).Encode(command))
		return body
	}

	t.Run("request too large", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, maxRequestSize: 100}
		command, _ := cpCommand()
		command.Files["large.txt"] = make([]byte, 1000)

		response := httptest.NewRecorder()
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command)))
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	})

	t.Run("canceled while all commands are running", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, running: make(chan struct{}, 1)}
		svc.running <- struct{}{} // Another command is running

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		command, _ := cpCommand()
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", encodeRequest(t, command))

		response := httptest.NewRecorder()
		svc.ServeHTTP(response, request)
		assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	})

	t.Run("runs when a command slot is free", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, running: make(chan struct{}, 1)}
		command, _ := cpCommand()

		response := httptest.NewRecorder()
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command)))
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Empty(t, svc.running, "slot is released")
	})

	t.Run("invalid request body", func(t *testing.T) {
		response := httptest.NewRecorder()
		(&service{}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("not gob")))
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})
}
