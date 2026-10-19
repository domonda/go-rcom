package rcom

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/domonda/golog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ungerik/go-fs"
)

// newTestClient starts an rcom service for cmd on a local test server
// and returns a Client configured for it with the additional options.
func newTestClient(t *testing.T, cmd string, options ...ClientOption) *Client {
	t.Helper()
	return newTestClientFor(t, &service{allowedCMDs: map[string]bool{cmd: true}}, cmd, options...)
}

// newTestClientFor starts svc on a local test server
// and returns a Client for cmd configured for it
func newTestClientFor(t *testing.T, svc *service, cmd string, options ...ClientOption) *Client {
	t.Helper()

	server := httptest.NewServer(svc)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	port, err := strconv.ParseUint(serverURL.Port(), 10, 16)
	require.NoError(t, err)

	options = append(options,
		ClientWithCmds(cmd),
		ClientWithHost(serverURL.Hostname()),
		ClientWithPort(uint16(port)),
	)
	return NewClient(options...)
}

func Test_Client_Execute(t *testing.T) {
	client := newTestClient(t, copyCmd())
	data := []byte("rcom client test file")

	localFile := fs.File(filepath.Join(t.TempDir(), "input.txt"))
	require.NoError(t, localFile.WriteAll(data))

	// Every input file is sent to the server under its base name,
	// so the command finds it as "input.txt" in its working directory.
	inputs := map[string]fs.FileReader{
		"MemFile":                   fs.NewMemFile("input.txt", data),
		"MemFile with slash path":   fs.NewMemFile("uploads/input.txt", data),
		"MemFile with Windows path": fs.NewMemFile(`C:\Users\me\input.txt`, data),
		// FileReaderWithName returns its name unchanged,
		// so only the client strips the directory part
		"FileReader named with slash path": fs.FileReaderWithName(fs.NewMemFile("other.txt", data), "uploads/input.txt"),
		"local File":                       localFile,
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			result, err := client.Execute(
				context.Background(),
				[]string{"input.txt", "output.txt"},
				[]fs.FileReader{input},
				"output.*",
			)
			require.NoError(t, err)

			assert.Equal(t, 7, result.CallID.Version(), "server's callID is transmitted to the client")
			assert.Equal(t, data, result.Files["output.txt"], "result file has the input content")
			assert.NotContains(t, result.Files, "input.txt", "input file does not match the result file pattern")
		})
	}

	t.Run("unreadable input file", func(t *testing.T) {
		missing := fs.File(filepath.Join(t.TempDir(), "missing.txt"))

		_, err := client.Execute(context.Background(), nil, []fs.FileReader{missing})
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("command not allowed by client", func(t *testing.T) {
		_, err := client.ExecuteWithCommand(context.Background(), "ls", nil, nil)
		assert.ErrorContains(t, err, `"ls"`)
	})

	t.Run("input files with the same base name", func(t *testing.T) {
		_, err := client.Execute(
			context.Background(),
			[]string{"input.txt", "output.txt"},
			[]fs.FileReader{fs.NewMemFile("a/input.txt", data), fs.NewMemFile(`C:\b\input.txt`, data)},
		)
		assert.ErrorContains(t, err, `more than one input file named "input.txt"`)
	})
}

func Test_Client_Execute_timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}

	client := newTestClient(t, "sleep", ClientWithTimeout(50*time.Millisecond))
	_, err := client.Execute(context.Background(), []string{"10"}, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, `rcom command "sleep" timed out after 50ms`)
}

func Test_Client_Execute_notOneCommand(t *testing.T) {
	client := NewClient(ClientWithCmds("convert", "ffmpeg"))
	assert.Panics(t, func() { _, _ = client.Execute(context.Background(), nil, nil) })
}

func Test_Client_addr(t *testing.T) {
	assert.Equal(t, "http://localhost:3666", NewClient(ClientWithHost("localhost"), ClientWithPort(3666)).addr())
	assert.Equal(t, "http://[::1]:3666", NewClient(ClientWithHost("::1"), ClientWithPort(3666)).addr())
	assert.Equal(t, "http://[::1]:3666", NewClient(ClientWithHost("[::1]"), ClientWithPort(3666)).addr(), "bracketed host")
}

func Test_Client_retries(t *testing.T) {
	setVar(t, &retryBaseDelay, time.Millisecond)

	// dropConnection as status closes the connection without a response,
	// so that the request fails like one to a restarting server
	const dropConnection = 0

	// statuses are the responses before the service handles the request,
	// requestIDs receives the request ID of every request
	var requestIDs []string
	newClient := func(t *testing.T, retries int, statuses ...int) (*Client, *int) {
		t.Helper()
		svc := &service{allowedCMDs: map[string]bool{copyCmd(): true}}
		var requests int
		requestIDs = nil
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			requestIDs = append(requestIDs, r.Header.Get(requestIDHeader))
			w.Header().Set(requestIDHeader, r.Header.Get(requestIDHeader))
			if requests <= len(statuses) {
				if statuses[requests-1] == dropConnection {
					conn, _, err := http.NewResponseController(w).Hijack()
					if err == nil {
						conn.Close()
					}
					return
				}
				http.Error(w, "unavailable", statuses[requests-1])
				return
			}
			svc.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		serverURL, err := url.Parse(server.URL)
		require.NoError(t, err)
		port, err := strconv.ParseUint(serverURL.Port(), 10, 16)
		require.NoError(t, err)
		client := NewClient(ClientWithCmds(copyCmd()), ClientWithHost(serverURL.Hostname()), ClientWithPort(uint16(port)), ClientWithRetries(retries))
		return client, &requests
	}
	execute := func(client *Client) error {
		_, err := client.Execute(context.Background(), []string{"input.txt", "output.txt"}, []fs.FileReader{fs.NewMemFile("input.txt", []byte("x"))})
		return err
	}

	client, requests := newClient(t, 3, http.StatusBadGateway, http.StatusServiceUnavailable)
	require.NoError(t, execute(client), "load balancer statuses are retried")
	assert.Equal(t, 3, *requests)
	_, err := golog.ParseUUID(requestIDs[0])
	assert.NoError(t, err, "the client creates a request ID")
	assert.Equal(t, []string{requestIDs[0], requestIDs[0], requestIDs[0]}, requestIDs, "one request ID for all attempts")

	client, requests = newClient(t, 1, http.StatusServiceUnavailable, http.StatusServiceUnavailable)
	err = execute(client)
	responseErr, ok := errors.AsType[*ResponseError](err)
	require.True(t, ok, "error %v is a ResponseError", err)
	assert.Equal(t, http.StatusServiceUnavailable, responseErr.StatusCode)
	assert.Equal(t, requestIDs[1], responseErr.RequestID)
	assert.Equal(t, 2, *requests, "one retry")

	client, requests = newClient(t, 3, http.StatusInternalServerError)
	assert.Error(t, execute(client))
	assert.Equal(t, 1, *requests, "a failed command is not retried")

	client, requests = newClient(t, 3, http.StatusGatewayTimeout)
	assert.Error(t, execute(client))
	assert.Equal(t, 1, *requests, "the server can still be executing the command")

	client, requests = newClient(t, 3, http.StatusOK)
	assert.Error(t, execute(client), "the error message is not a result")
	assert.Equal(t, 1, *requests, "an invalid result is not retried")

	client, requests = newClient(t, 1, dropConnection)
	require.NoError(t, execute(client), "a request without a response is retried")
	assert.Equal(t, 2, *requests)

	t.Run("timeout ends the delay before a retry", func(t *testing.T) {
		setVar(t, &retryBaseDelay, time.Minute)
		client, requests := newClient(t, 3, http.StatusServiceUnavailable)
		ClientWithTimeout(100 * time.Millisecond)(client)

		executed := make(chan error, 1)
		go func() { executed <- execute(client) }()
		select {
		case err := <-executed:
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			_, ok := errors.AsType[*ResponseError](err)
			assert.True(t, ok, "error %v contains the error of the last attempt", err)
		case <-time.After(10 * time.Second):
			t.Fatal("still waiting to retry after the timeout of the client")
		}
		assert.Equal(t, 1, *requests)
	})
}

func Test_isRetryable(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	noResponse := &transportError{err: errors.New("connection refused")}
	for name, tc := range map[string]struct {
		ctx       context.Context
		err       error
		retryable bool
	}{
		"no response":            {context.Background(), noResponse, true},
		"wrapped no response":    {context.Background(), fmt.Errorf("x: %w", noResponse), true},
		"bad gateway":            {context.Background(), &ResponseError{StatusCode: http.StatusBadGateway}, true},
		"service unavailable":    {context.Background(), &ResponseError{StatusCode: http.StatusServiceUnavailable}, true},
		"gateway timeout":        {context.Background(), &ResponseError{StatusCode: http.StatusGatewayTimeout}, false},
		"failed command":         {context.Background(), &ResponseError{StatusCode: http.StatusInternalServerError}, false},
		"invalid command":        {context.Background(), &ResponseError{StatusCode: http.StatusBadRequest}, false},
		"invalid result":         {context.Background(), errors.New("rcom.Result: unexpected EOF"), false},
		"context ended":          {canceled, noResponse, false},
		"context ended with 503": {canceled, &ResponseError{StatusCode: http.StatusServiceUnavailable}, false},
	} {
		assert.Equal(t, tc.retryable, isRetryable(tc.ctx, tc.err), name)
	}
}

func Test_retryDelay(t *testing.T) {
	setVar(t, &retryBaseDelay, time.Second)
	for range 100 {
		delay := retryDelay(4) // Fibonacci number 3
		assert.GreaterOrEqual(t, delay, 1500*time.Millisecond)
		assert.LessOrEqual(t, delay, 3*time.Second)
	}
}

func Test_fibonacci(t *testing.T) {
	for n, expected := range []int{0, 1, 1, 2, 3, 5, 8} {
		assert.Equal(t, expected, fibonacci(n))
	}
}
