package rcom

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ExecuteLocally_callIDIsUUIDv7(t *testing.T) {
	command, _ := cpCommand()

	// UUIDv7 timestamps have millisecond precision
	before := time.Now().Truncate(time.Millisecond)
	result, callID, err := ExecuteLocally(context.Background(), command)
	after := time.Now()
	require.NoError(t, err)
	require.Equal(t, callID, result.CallID)

	// The UUIDv7 timestamp is the time the call started,
	// which makes call IDs sort by time
	started, ok := callID.V7Time()
	require.True(t, ok, "callID is a version 7 UUID")
	assert.False(t, started.Before(before), "callID time %s is not before the call (%s)", started, before)
	assert.False(t, started.After(after), "callID time %s is not after the call (%s)", started, after)
}

func Test_ExecuteLocally_fileNameOutsideWorkingDir(t *testing.T) {
	// The default TempDir is the parent of the working dir
	// that an escaping file name would write into
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("TEMP", tempDir)

	command, _ := cpCommand()
	command.Files["..%2Fescaped.txt"] = []byte("escaped")

	_, _, err := ExecuteLocally(context.Background(), command)
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(tempDir, "escaped.txt"))
}

func Test_ExecuteLocally_symlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses ln")
	}

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside"), 0o600))

	// The command creates a symlink to a directory and one to a file
	// outside of its working dir
	command := &Command{
		Name: "sh",
		Args: []string{"-c", `ln -s "$0" dir-link && ln -s "$1" file-link.txt`, outsideDir, outsideFile},
	}
	result, _, err := ExecuteLocally(context.Background(), command)
	require.NoError(t, err)

	assert.NotContains(t, result.Files, "file-link.txt", "symlinked file is not a result file")
	assert.FileExists(t, outsideFile, "cleanup does not delete through the symlinked dir")
}

func Test_ExecuteLocally_NonErrorExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}

	command := &Command{
		Name:              "sh",
		Args:              []string{"-c", "echo failed; exit 1"},
		NonErrorExitCodes: map[int]bool{1: true},
	}

	// A listed exit code is returned as Result.ExitCode
	result, _, err := ExecuteLocally(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ExitCode)

	// Any other non-zero exit code is an error with the exit code and output
	for _, nonErrorExitCodes := range []map[int]bool{nil, {2: true}} {
		command.NonErrorExitCodes = nonErrorExitCodes
		_, _, err = ExecuteLocally(context.Background(), command)
		require.Error(t, err)
		assert.ErrorContains(t, err, "exited with code 1")
		assert.ErrorContains(t, err, "failed")
	}
}
