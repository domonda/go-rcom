//go:build unix

package rcom

import (
	"bufio"
	"bytes"
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
	require.NoError(t, writeWireCommand(body, command))
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
	assert.Equal(t, []string{"chunked"}, response.TransferEncoding, "a body that ends early is detected")

	result, err := readWireResult(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", result.Stdout)
}

func Test_transferLimits_deadline(t *testing.T) {
	assert.True(t, (&transferLimits{}).deadline(1).IsZero(), "no limits")

	limits := transferLimits{timeout: time.Second, minRate: 1000}
	first := limits.deadline(1)
	assert.WithinDuration(t, time.Now().Add(time.Second), first, 100*time.Millisecond, "stall deadline")

	// After 1000 bytes the minimum rate allows one more second from the start,
	// later than the stall deadline
	limits.done = 3000
	assert.WithinDuration(t, time.Now().Add(time.Second), limits.deadline(1), 100*time.Millisecond)

	// A transfer behind the minimum rate gets the earlier rate deadline
	limits.start = time.Now().Add(-10 * time.Second)
	limits.done = 1000
	assert.WithinDuration(t, limits.start.Add(2*time.Second), limits.deadline(1), 100*time.Millisecond)
}

func Test_stallWriter_chunks(t *testing.T) {
	var deadlines int
	buf := bytes.NewBuffer(nil)
	w := &stallWriter{
		w:           buf,
		setDeadline: func(time.Time) error { deadlines++; return nil },
		transfer:    transferLimits{timeout: time.Minute},
	}
	n, err := w.Write(make([]byte, 3*stallWriteChunkSize+1))
	require.NoError(t, err)
	assert.Equal(t, 3*stallWriteChunkSize+1, n)
	assert.Equal(t, 4, deadlines, "one deadline per chunk")
	assert.Equal(t, n, buf.Len())
}

func Test_service_slowReaderGetsLargeResult(t *testing.T) {
	// The transfer takes longer than StallTimeout,
	// but the client never stops receiving for that long
	svc := &service{allowedCMDs: map[string]bool{"head": true}, stallTimeout: 300 * time.Millisecond}
	addr, _ := startObservedServer(t, svc)

	conn := sendRawRequest(t, addr, &Command{
		Name:               "head",
		Args:               []string{"-c", "20000000", "/dev/zero"},
		ResultFilePatterns: []string{"none"},
	})
	reader := &slowReader{r: conn, chunk: 256 << 10, pause: 20 * time.Millisecond}
	response, err := http.ReadResponse(bufio.NewReader(reader), nil)
	require.NoError(t, err)
	result, err := readWireResult(response.Body)
	require.NoError(t, err)
	assert.Len(t, result.Stdout, 20000000)
}

// slowReader reads at most chunk bytes after every pause
type slowReader struct {
	r     io.Reader
	chunk int
	pause time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(s.pause)
	return s.r.Read(p[:min(len(p), s.chunk)])
}

func Test_service_MinTransferRate(t *testing.T) {
	svc := &service{
		allowedCMDs:     map[string]bool{"echo": true},
		stallTimeout:    200 * time.Millisecond,
		minTransferRate: 1000,
	}
	addr, finished := startObservedServer(t, svc)

	// The client sends one byte every 50 ms, which never stalls
	// for StallTimeout but stays below MinTransferRate
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: rcom\r\nContent-Length: 100000\r\n\r\n")
	require.NoError(t, err)
	go func() {
		for range 200 {
			if _, err := conn.Write([]byte{0}); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	requireFinished(t, finished, "still reading a request slower than MinTransferRate")
}

func Test_service_lineBreakAfterRequest(t *testing.T) {
	svc := &service{allowedCMDs: map[string]bool{"sh": true}}
	addr, _ := startObservedServer(t, svc)

	// HTTP/1.1 tolerates line breaks after a request body,
	// which must not cancel the command
	conn := sendRawRequest(t, addr, &Command{Name: "sh", Args: []string{"-c", "sleep 0.2; echo done"}})
	_, err := conn.Write([]byte("\r\n"))
	require.NoError(t, err)

	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	result, err := readWireResult(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "done\n", result.Stdout)
}

func Test_service_inflight(t *testing.T) {
	svc := &service{
		allowedCMDs:  map[string]bool{copyCmd(): true},
		inflight:     make(chan struct{}, 1),
		shuttingDown: make(chan struct{}),
	}
	svc.inflight <- struct{}{} // Another request is sending its result
	svc.shutdown()
	command, _ := cpCommand()
	body := bytes.NewBuffer(nil)
	require.NoError(t, writeWireCommand(body, command))

	response := httptest.NewRecorder()
	svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", body))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func Test_ExecuteRemotely_errorOutputTail(t *testing.T) {
	server := httptest.NewServer(&service{allowedCMDs: map[string]bool{"sh": true}})
	defer server.Close()

	command := &Command{Name: "sh", Args: []string{"-c", "yes | head -c 200000; echo last line; exit 1"}}
	_, err := ExecuteRemotely(t.Context(), server.URL, command)
	require.Error(t, err)
	assert.True(t, strings.HasSuffix(err.Error(), "last line"), "error ends with the output tail")
}

func Test_service_AllowedCommandEnvVars(t *testing.T) {
	svc := &service{allowedCMDs: map[string]bool{"sh": true}, allowedEnvVars: []string{"OMP_THREAD_LIMIT"}}
	serve := func(env map[string]string) *httptest.ResponseRecorder {
		body := bytes.NewBuffer(nil)
		require.NoError(t, writeWireCommand(body, &Command{Name: "sh", Args: []string{"-c", `printf %s "$OMP_THREAD_LIMIT"`}, Env: env}))
		response := httptest.NewRecorder()
		svc.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", body))
		return response
	}

	response := serve(map[string]string{"OMP_THREAD_LIMIT": "1"})
	require.Equal(t, http.StatusOK, response.Code)
	result, err := readWireResult(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "1", result.Stdout)

	// Variables like LD_PRELOAD would let a client run any code
	response = serve(map[string]string{"LD_PRELOAD": "evil.so"})
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), `environment variable "LD_PRELOAD" not allowed`)
}
