package rcom

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ungerik/go-fs"
)

// newTestClient starts an rcom service for cmd on a local test server
// and returns a Client configured for it with the additional options.
func newTestClient(t *testing.T, cmd string, options ...ClientOption) *Client {
	t.Helper()

	server := httptest.NewServer(&service{allowedCMDs: map[string]bool{cmd: true}})
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

	client := newTestClient(t, "sleep", ClientWithTimeOut(50*time.Millisecond))
	_, err := client.Execute(context.Background(), []string{"10"}, nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
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
