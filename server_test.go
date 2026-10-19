package rcom

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/domonda/golog"
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

	// Requests still running are canceled and their handlers
	// have cleaned up when serveUntilSignal returns
	canceled := map[string]struct {
		timeout  time.Duration
		signals  int
		expected error
	}{
		"after the timeout":                     {timeout: time.Millisecond, signals: 1, expected: context.DeadlineExceeded},
		"after a second signal":                 {timeout: time.Minute, signals: 2, expected: context.Canceled},
		"after a second signal without timeout": {timeout: 0, signals: 2, expected: context.Canceled},
	}
	for name, tc := range canceled {
		t.Run("cancels requests "+name, func(t *testing.T) {
			addr, signals, started, finished, served := startSignaledServer(t, tc.timeout)

			requestErr := make(chan error, 1)
			go func() {
				_, err := ExecuteRemotely(context.Background(), addr, &Command{Name: "sleep", Args: []string{"10"}})
				requestErr <- err
			}()
			<-started
			for range tc.signals {
				// Blocks until the previous signal was received
				signals <- syscall.SIGTERM
			}

			assert.ErrorIs(t, <-served, tc.expected)
			select {
			case <-finished:
			default:
				t.Error("returned before the canceled request was cleaned up")
			}
			assert.Error(t, <-requestErr, "canceled request")
		})
	}
}

func Test_newServer(t *testing.T) {
	server := newServer(3666, []string{"convert"})
	assert.Equal(t, ":3666", server.Addr)
	svc := server.Handler.(*service)
	assert.Equal(t, map[string]bool{"convert": true}, svc.allowedCMDs)

	// Shutdown rejects requests waiting for a command slot
	require.NoError(t, server.Shutdown(context.Background()))
	select {
	case <-svc.shuttingDown:
	case <-time.After(5 * time.Second):
		t.Error("Shutdown did not call service.shutdown")
	}
}

func Test_ListenAndServe_relativeCommand(t *testing.T) {
	err := ListenAndServe(0, false, "convert", "./convert")
	assert.ErrorContains(t, err, "nor an absolute path")
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

	setVar(t, &MaxRequestMemory, 1000)
	assert.Equal(t, int64(1000), newService(nil).memory.size)
	MaxRequestMemory = 0
	assert.Nil(t, newService(nil).memory, "no memory limit")

	setVar(t, &AllowedCommandEnvVars, []string{"TZ"})
	svc = newService(nil)
	AllowedCommandEnvVars[0] = "LD_PRELOAD"
	assert.Equal(t, []string{"TZ"}, svc.allowedEnvVars, "a copy of AllowedCommandEnvVars")
}

func Test_service_limits(t *testing.T) {
	encodeRequest := func(t *testing.T, command *Command) *bytes.Buffer {
		t.Helper()
		body := bytes.NewBuffer(nil)
		require.NoError(t, writeWireCommand(body, command))
		return body
	}

	t.Run("request too large", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, maxRequestSize: 100}
		command, _ := cpCommand()
		command.Files["large.txt"] = make([]byte, 1000)

		// Rejected by its Content-Length without reading it
		encoded := encodeRequest(t, command)
		body := &countingReader{r: encoded}
		request := httptest.NewRequest(http.MethodPost, "/", body)
		request.ContentLength = int64(encoded.Len())
		response := httptest.NewRecorder()
		svc.ServeHTTP(response, request)
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
		assert.Zero(t, body.bytesRead(), "bytes read")

		// A request of unknown size is rejected after MaxRequestSize bytes
		request = httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command))
		request.ContentLength = -1
		response = httptest.NewRecorder()
		svc.ServeHTTP(response, request)
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	})

	t.Run("waits unread for memory", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, memory: newMemoryBudget(1 << 20)}
		release, err := svc.memory.reserve(t.Context(), nil, 1<<20) // Other requests
		require.NoError(t, err)
		command, _ := cpCommand()
		encoded := encodeRequest(t, command)
		body := &countingReader{r: encoded}
		request := httptest.NewRequest(http.MethodPost, "/", body)
		request.ContentLength = int64(encoded.Len())

		response := httptest.NewRecorder()
		served := make(chan struct{})
		go func() {
			defer close(served)
			svc.ServeHTTP(response, request)
		}()
		select {
		case <-served:
			t.Fatalf("served with status %d while the memory budget was used", response.Code)
		case <-time.After(100 * time.Millisecond):
		}
		assert.Zero(t, body.bytesRead(), "bytes read while waiting")

		release()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Fatal("still waiting after memory became free")
		}
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Zero(t, svc.memory.reservedBytes(), "memory is released")
	})

	t.Run("waits for memory at most StallTimeout", func(t *testing.T) {
		// net/http doesn't notice that a client closed
		// the connection of a request that wasn't read
		svc := &service{
			allowedCMDs:  map[string]bool{copyCmd(): true},
			memory:       newMemoryBudget(1),
			stallTimeout: 100 * time.Millisecond,
		}
		release, err := svc.memory.reserve(t.Context(), nil, 1)
		require.NoError(t, err)
		defer release()
		command, _ := cpCommand()
		request := httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command))

		response := httptest.NewRecorder()
		served := make(chan struct{})
		go func() {
			defer close(served)
			svc.ServeHTTP(response, request)
		}()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Fatal("still waiting for memory after StallTimeout")
		}
		assert.Equal(t, http.StatusServiceUnavailable, response.Code, "clients can retry")
		assert.Contains(t, response.Body.String(), "waited 100ms")
		assert.Equal(t, int64(1), svc.memory.reservedBytes(), "the waiting request reserved nothing")
	})

	t.Run("rejected while waiting for memory during shutdown", func(t *testing.T) {
		svc := &service{
			allowedCMDs:  map[string]bool{copyCmd(): true},
			memory:       newMemoryBudget(1),
			shuttingDown: make(chan struct{}),
		}
		release, err := svc.memory.reserve(t.Context(), nil, 1)
		require.NoError(t, err)
		defer release()
		svc.shutdown()
		command, _ := cpCommand()
		request := httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command))

		response := httptest.NewRecorder()
		served := make(chan struct{})
		go func() {
			defer close(served)
			svc.ServeHTTP(response, request)
		}()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Fatal("still waiting for memory while shutting down")
		}
		assert.Equal(t, http.StatusServiceUnavailable, response.Code)
		assert.Contains(t, response.Body.String(), "shutting down")
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

	t.Run("waits for a free command slot", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, running: make(chan struct{}, 1)}
		svc.running <- struct{}{} // Another command is running
		command, _ := cpCommand()
		request := httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command))

		response := httptest.NewRecorder()
		served := make(chan struct{})
		go func() {
			defer close(served)
			svc.ServeHTTP(response, request)
		}()
		select {
		case <-served:
			t.Fatalf("served with status %d while all command slots were taken", response.Code)
		case <-time.After(100 * time.Millisecond):
		}

		<-svc.running // The other command finished
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Fatal("still waiting after a command slot became free")
		}
		assert.Equal(t, http.StatusOK, response.Code)
	})

	t.Run("slot and memory are released before the result is sent", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, running: make(chan struct{}, 1), memory: newMemoryBudget(1 << 20)}
		command, _ := cpCommand()

		response := &slotCheckingWriter{ResponseRecorder: httptest.NewRecorder(), svc: svc}
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command)))
		require.Equal(t, http.StatusOK, response.Code)
		assert.False(t, response.slotTakenWhileWriting, "a slow client would block other commands")
		assert.False(t, response.memoryReservedWhileWriting, "a slow client would block other requests")
	})

	t.Run("invalid command", func(t *testing.T) {
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}}
		command, _ := cpCommand()
		command.Files["..%2Fescaped.txt"] = nil

		response := httptest.NewRecorder()
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command)))
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, response.Body.String(), "path separators")
	})

	t.Run("rejected while shutting down", func(t *testing.T) {
		svc := &service{
			allowedCMDs:  map[string]bool{copyCmd(): true},
			running:      make(chan struct{}, 1),
			shuttingDown: make(chan struct{}),
		}
		svc.running <- struct{}{} // Another command is running
		svc.shutdown()
		command, _ := cpCommand()

		response := httptest.NewRecorder()
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command)))
		assert.Equal(t, http.StatusServiceUnavailable, response.Code)
		assert.Contains(t, response.Body.String(), "shutting down")
	})

	t.Run("free slot is taken while shutting down", func(t *testing.T) {
		svc := &service{
			allowedCMDs:  map[string]bool{copyCmd(): true},
			running:      make(chan struct{}, 1),
			shuttingDown: make(chan struct{}),
		}
		svc.shutdown()

		// A select between a free slot and the shutdown would choose randomly
		for range 50 {
			command, _ := cpCommand()
			_, _, err := svc.execute(context.Background(), command)
			require.NoError(t, err)
		}
	})

	t.Run("data after the command", func(t *testing.T) {
		// Less data than the read buffer of gob is detected too,
		// which gob would consume if budgetReader had no ReadByte
		for _, size := range []int{1, 64 << 10} {
			svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}}
			command, _ := cpCommand()
			body := encodeRequest(t, command)
			body.Write(make([]byte, size))

			response := httptest.NewRecorder()
			svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", body))
			assert.Equal(t, http.StatusBadRequest, response.Code, "%d bytes after the command", size)
			assert.Contains(t, response.Body.String(), "unexpected data after the command")
		}
	})

	t.Run("file names differing in case", func(t *testing.T) {
		// Detect if the temp dir file system ignores case
		probe := filepath.Join(t.TempDir(), "CASE")
		require.NoError(t, os.WriteFile(probe, nil, 0o600))
		if _, err := os.Stat(filepath.Join(filepath.Dir(probe), "case")); err != nil {
			t.Skip("case-sensitive file system")
		}

		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}}
		command, _ := cpCommand()
		command.Files["A.txt"] = nil
		command.Files["a.txt"] = nil

		response := httptest.NewRecorder()
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", encodeRequest(t, command)))
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("invalid request body", func(t *testing.T) {
		response := httptest.NewRecorder()
		(&service{}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("not gob")))
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})
}

// slotCheckingWriter records if a command slot of svc is taken,
// or memory reserved, while the response is written
type slotCheckingWriter struct {
	*httptest.ResponseRecorder
	svc                        *service
	slotTakenWhileWriting      bool
	memoryReservedWhileWriting bool
}

func (w *slotCheckingWriter) Write(p []byte) (int, error) {
	if len(w.svc.running) > 0 {
		w.slotTakenWhileWriting = true
	}
	if w.svc.memory != nil && w.svc.memory.reservedBytes() > 0 {
		w.memoryReservedWhileWriting = true
	}
	return w.ResponseRecorder.Write(p)
}

// countingReader counts the bytes read from r
type countingReader struct {
	r  io.Reader
	mu sync.Mutex
	n  int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.mu.Lock()
	c.n += n
	c.mu.Unlock()
	return n, err
}

func (c *countingReader) bytesRead() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func Test_activeHandlers(t *testing.T) {
	var handlers activeHandlers
	require.NoError(t, handlers.wait(t.Context()), "no handlers")

	handlers.add()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, handlers.wait(ctx), context.DeadlineExceeded)

	// Handlers can start while another goroutine waits
	waited := make(chan error, 1)
	go func() { waited <- handlers.wait(t.Context()) }()
	handlers.add()
	handlers.done()
	handlers.done()
	assert.NoError(t, <-waited)
}

func Test_service_health(t *testing.T) {
	response := httptest.NewRecorder()
	(&service{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, healthPath, nil))
	assert.Equal(t, http.StatusOK, response.Code)

	response = httptest.NewRecorder()
	(&service{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, response.Code, "commands need POST")
	assert.Equal(t, http.MethodPost, response.Header().Get("Allow"))

	// The server responds without waiting for a body it doesn't read,
	// which a client may never send
	server := httptest.NewServer(&service{})
	defer server.Close()
	for request, status := range map[string]int{"GET /health": http.StatusOK, "GET /": http.StatusMethodNotAllowed} {
		conn, err := net.Dial("tcp", server.Listener.Addr().String())
		require.NoError(t, err)
		defer conn.Close()
		_, err = fmt.Fprintf(conn, "%s HTTP/1.1\r\nHost: rcom\r\nContent-Length: 1000\r\n\r\n", request)
		require.NoError(t, err)
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		require.NoError(t, err, "%s: response without the body", request)
		assert.Equal(t, status, response.StatusCode, request)
		assert.True(t, response.Close, "%s: the connection with the unread body is closed", request)
	}
}

func Test_service_AllowedCommandEnvVars(t *testing.T) {
	serve := func(allowedEnvVars []string, env map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}, allowedEnvVars: allowedEnvVars}
		command, _ := cpCommand()
		command.Env = env
		body := bytes.NewBuffer(nil)
		require.NoError(t, writeWireCommand(body, command))
		response := httptest.NewRecorder()
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", body))
		return response
	}

	response := serve([]string{"OMP_THREAD_LIMIT"}, map[string]string{"OMP_THREAD_LIMIT": "1"})
	assert.Equal(t, http.StatusOK, response.Code)

	// Variables like LD_PRELOAD would let a client run any code
	response = serve([]string{"OMP_THREAD_LIMIT"}, map[string]string{"LD_PRELOAD": "evil.so"})
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), `environment variable "LD_PRELOAD" not allowed`)

	for name, allowedEnvVars := range map[string][]string{"nil": nil, "empty": {}} {
		response = serve(allowedEnvVars, map[string]string{"OMP_THREAD_LIMIT": "1"})
		assert.Equal(t, http.StatusBadRequest, response.Code, "%s allowlist allows no variables", name)
	}
}

func Test_service_canceledByShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}

	svc := &service{allowedCMDs: map[string]bool{"sleep": true}, shuttingDown: make(chan struct{})}
	body := bytes.NewBuffer(nil)
	require.NoError(t, writeWireCommand(body, &Command{Name: "sleep", Args: []string{"10"}}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	response := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		svc.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodPost, "/", body))
	}()

	// serveUntilSignal cancels the requests still running at the end of a shutdown
	time.Sleep(50 * time.Millisecond)
	svc.shutdown()
	cancel()
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("command still running after it was canceled")
	}
	assert.Equal(t, http.StatusServiceUnavailable, response.Code, "another server can run the command")
}

func Test_service_requestID(t *testing.T) {
	requestID := golog.UUIDv4()
	var receivedRequestID string
	svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRequestID = r.Header.Get("X-Request-ID")
		svc.ServeHTTP(w, r)
	}))
	defer server.Close()

	// ExecuteRemotely passes on the request ID of the context
	ctx := golog.ContextWithRequestUUID(context.Background(), requestID)
	command, _ := cpCommand()
	_, err := ExecuteRemotely(ctx, server.URL, command)
	require.NoError(t, err)
	assert.Equal(t, golog.FormatUUID(requestID), receivedRequestID)

	_, err = ExecuteRemotely(golog.ContextWithRequestID(context.Background(), "job-42"), server.URL, command)
	require.NoError(t, err)
	assert.Equal(t, "job-42", receivedRequestID, "string request ID")

	// The server responds with the request ID, or a new one
	body := bytes.NewBuffer(nil)
	require.NoError(t, writeWireCommand(body, &Command{Name: "not-allowed"}))
	request := httptest.NewRequest(http.MethodPost, "/", body)
	request.Header.Set("X-Request-ID", golog.FormatUUID(requestID))
	response := httptest.NewRecorder()
	svc.ServeHTTP(response, request)
	assert.Equal(t, golog.FormatUUID(requestID), response.Header().Get("X-Request-ID"))

	response = httptest.NewRecorder()
	svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("x")))
	_, err = golog.ParseUUID(response.Header().Get("X-Request-ID"))
	assert.NoError(t, err, "new request ID")

	// The response written to the hijacked connection
	// of a real server has the request ID too
	commandBody := bytes.NewBuffer(nil)
	require.NoError(t, writeWireCommand(commandBody, command))
	hijackedRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, commandBody)
	require.NoError(t, err)
	hijackedRequest.Header.Set("X-Request-ID", golog.FormatUUID(requestID))
	hijackedResponse, err := http.DefaultClient.Do(hijackedRequest)
	require.NoError(t, err)
	defer hijackedResponse.Body.Close()
	assert.Equal(t, http.StatusOK, hijackedResponse.StatusCode)
	assert.Equal(t, golog.FormatUUID(requestID), hijackedResponse.Header.Get("X-Request-ID"))
}

func Test_service_logsRequestID(t *testing.T) {
	var logged bytes.Buffer
	setVar(t, &log, golog.NewLogger(golog.NewConfig(&golog.DefaultLevels, golog.AllLevelsActive, golog.NewJSONWriterConfig(&logged, nil))))

	requestID := golog.FormatUUID(golog.UUIDv4())
	command, _ := cpCommand()
	body := bytes.NewBuffer(nil)
	require.NoError(t, writeWireCommand(body, command))
	request := httptest.NewRequest(http.MethodPost, "/", body)
	request.Header.Set("X-Request-ID", requestID)
	response := httptest.NewRecorder()
	(&service{allowedCMDs: map[string]bool{copyCmd(): true}}).ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, logged.String(), requestID)
}
