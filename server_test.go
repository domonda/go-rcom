package rcom

import (
	"context"
	"net"
	"net/http"
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
