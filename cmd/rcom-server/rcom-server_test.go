package main

import (
	"bytes"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_parseArgs(t *testing.T) {
	port, cmds, err := parseArgs([]string{"8080", "convert", "ffmpeg"}, "9000")
	require.NoError(t, err)
	assert.Equal(t, uint16(8080), port)
	assert.Equal(t, []string{"convert", "ffmpeg"}, cmds)

	port, _, err = parseArgs([]string{"", "convert"}, "9000")
	require.NoError(t, err)
	assert.Equal(t, uint16(9000), port, "PORT environment variable")

	port, _, err = parseArgs([]string{"", "convert"}, "")
	require.NoError(t, err)
	assert.Equal(t, uint16(3666), port, "default port")

	for _, args := range [][]string{nil, {"8080"}, {"70000", "convert"}, {"-1", "convert"}, {"port", "convert"}} {
		_, _, err = parseArgs(args, "")
		assert.Error(t, err, "%q", args)
	}
}

func Test_checkCommands(t *testing.T) {
	assert.NoError(t, checkCommands([]string{"go"}))
	assert.Error(t, checkCommands([]string{"go", "rcom-command-that-does-not-exist"}))

	goPath, err := exec.LookPath("go")
	require.NoError(t, err)
	absGoPath, err := filepath.Abs(goPath)
	require.NoError(t, err)
	assert.NoError(t, checkCommands([]string{absGoPath}), "absolute path")

	// The server would run a relative path in the working directory
	// of the command, where the client puts its input files
	for _, cmd := range []string{"./go", "bin/go"} {
		assert.ErrorContains(t, checkCommands([]string{cmd}), "nor an absolute path", cmd)
	}
}

func Test_run(t *testing.T) {
	var stderr bytes.Buffer
	assert.Equal(t, 2, run(nil, "", &stderr), "missing arguments")
	assert.Contains(t, stderr.String(), "usage: rcom-server PORT ALLOWED_COMMAND...")

	assert.Equal(t, 1, run([]string{"0", "rcom-command-that-does-not-exist"}, "", io.Discard), "command not found")

	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	assert.Equal(t, 1, run([]string{port, "go"}, "", io.Discard), "port in use")
}
