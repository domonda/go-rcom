package rcom

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func Test_ExecuteLocally_fileNamesDifferingInCase(t *testing.T) {
	// Detect if the temp dir file system ignores case
	probe := filepath.Join(t.TempDir(), "CASE")
	require.NoError(t, os.WriteFile(probe, nil, 0o600))
	_, err := os.Stat(filepath.Join(filepath.Dir(probe), "case"))
	caseInsensitive := err == nil

	command, _ := cpCommand()
	command.Files["A.txt"] = []byte("upper")
	command.Files["a.txt"] = []byte("lower")
	result, _, err := ExecuteLocally(context.Background(), command)
	if caseInsensitive {
		// One file would silently overwrite the other
		assert.ErrorIs(t, err, os.ErrExist)
		return
	}
	require.NoError(t, err)
	assert.Equal(t, []byte("upper"), result.Files["A.txt"])
	assert.Equal(t, []byte("lower"), result.Files["a.txt"])
}

func Test_readResultFiles_maxWireFiles(t *testing.T) {
	dir := t.TempDir()
	for i := range maxWireFiles + 1 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d.txt", i)), nil, 0o600))
	}
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()

	// The client would reject a result with more files
	_, err = readResultFiles(t.Context(), root, []string{"*.txt"}, 0)
	assert.ErrorContains(t, err, fmt.Sprintf("more than %d result files", maxWireFiles))

	// No file matches, so only the check of the context stops it
	// from opening every file for its hard links
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = readResultFiles(ctx, root, []string{"none"}, 0)
	assert.ErrorIs(t, err, context.Canceled)

	files, err := readResultFiles(t.Context(), root, []string{"0*.txt"}, 0)
	require.NoError(t, err)
	assert.Len(t, files, maxWireFiles)
}

func Test_readLimited(t *testing.T) {
	for name, tc := range map[string]struct {
		data     string
		size     int64 // Expected size, can differ from the data
		limit    int64
		expected []byte
	}{
		"empty":                   {"", 0, -1, []byte{}},
		"no limit":                {"abc", 3, -1, []byte("abc")},
		"larger than expected":    {"abcdef", 2, -1, []byte("abcdef")},
		"smaller than expected":   {"ab", 10, -1, []byte("ab")},
		"at the limit":            {"abc", 3, 3, []byte("abc")},
		"over the limit":          {"abcd", 4, 3, nil},
		"expected over the limit": {"ab", 4, 3, nil},
		"grew over the limit":     {"abcd", 2, 3, nil},
		"sparse file":             {"ab", 1 << 50, -1, []byte("ab")},
	} {
		data, err := readLimited(strings.NewReader(tc.data), tc.size, tc.limit)
		require.NoError(t, err, name)
		assert.Equal(t, tc.expected, data, name)
	}
}
