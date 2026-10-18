//go:build unix

package rcom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domonda/go-rcom/pkg/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setVar sets a package variable for the duration of the test
func setVar[T any](t *testing.T, variable *T, value T) {
	t.Helper()
	previous := *variable
	*variable = value
	t.Cleanup(func() { *variable = previous })
}

func Test_ExecuteLocally_MaxOutputSize(t *testing.T) {
	setVar(t, &MaxOutputSize, 1<<20)

	// yes writes until it is killed
	_, _, err := ExecuteLocally(context.Background(), &Command{Name: "yes"})
	assert.ErrorIs(t, err, exec.ErrOutputTooLarge)
}

func Test_ExecuteLocally_errorOutputTail(t *testing.T) {
	command := &Command{Name: "sh", Args: []string{"-c", "yes | head -c 200000; echo last line; exit 1"}}

	_, _, err := ExecuteLocally(context.Background(), command)
	require.Error(t, err)
	assert.Less(t, len(err.Error()), maxErrorOutputSize+200, "error contains only the end of the output")
	assert.Contains(t, err.Error(), "bytes omitted]")
	assert.True(t, strings.HasSuffix(err.Error(), "last line\n"))
}

func Test_ExecuteLocally_MaxResultFilesSize(t *testing.T) {
	setVar(t, &MaxResultFilesSize, 10)

	command, _ := cpCommand() // Copies a 14 byte file
	_, _, err := ExecuteLocally(context.Background(), command)
	assert.ErrorContains(t, err, "result files larger than 10 bytes")

	setVar(t, &MaxResultFilesSize, 28)
	result, _, err := ExecuteLocally(context.Background(), command)
	require.NoError(t, err, "input and output file are 28 bytes")
	assert.Len(t, result.Files, 2)
}

func Test_ExecuteLocally_hardLinks(t *testing.T) {
	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside"), 0o600))

	// The working dir and outsideFile are in the same temp dir file system.
	// Hard links between files inside of the working dir are fine.
	command := &Command{
		Name: "sh",
		Args: []string{"-c", `ln "$0" link.txt && echo result > result.txt && ln result.txt result2.txt`, outsideFile},
	}
	result, _, err := ExecuteLocally(context.Background(), command)
	require.NoError(t, err)
	assert.NotContains(t, result.Files, "link.txt", "hard link to a file outside of the working dir")
	assert.Equal(t, []byte("result\n"), result.Files["result.txt"])
	assert.Equal(t, []byte("result\n"), result.Files["result2.txt"])
}

func Test_ExecuteLocally_fifo(t *testing.T) {
	// Opening a FIFO for reading would block until it has a writer
	command := &Command{Name: "sh", Args: []string{"-c", "mkfifo fifo.txt && echo result > result.txt"}}
	result, _, err := ExecuteLocally(context.Background(), command)
	require.NoError(t, err)
	assert.NotContains(t, result.Files, "fifo.txt")
	assert.Contains(t, result.Files, "result.txt")
}

func Test_ExecuteLocally_signal(t *testing.T) {
	_, _, err := ExecuteLocally(context.Background(), &Command{Name: "sh", Args: []string{"-c", "kill -9 $$"}})
	assert.ErrorContains(t, err, "ended by signal: killed")
}

func Test_ExecuteLocally_canceledWhileCopyingFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command, _ := cpCommand()
	_, _, err := ExecuteLocally(ctx, command)
	assert.ErrorIs(t, err, context.Canceled)
}

func Test_ExecuteLocally_InheritedEnvVars(t *testing.T) {
	t.Setenv("RCOM_TEST_SECRET", "secret")
	command := &Command{Name: "sh", Args: []string{"-c", `printf '%s:%s' "$RCOM_TEST_SECRET" "$PATH"`}}

	result, _, err := ExecuteLocally(context.Background(), command)
	require.NoError(t, err)
	secret, path, _ := strings.Cut(result.Stdout, ":")
	assert.Empty(t, secret, "variable not in InheritedEnvVars")
	assert.Equal(t, os.Getenv("PATH"), path)

	setVar(t, &InheritedEnvVars, nil)
	result, _, err = ExecuteLocally(context.Background(), command)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(result.Stdout, "secret:"), "nil inherits all variables")
}

func Test_ExecuteLocally_permissions(t *testing.T) {
	command, _ := cpCommand()
	command.Name = "ls"
	command.Args = []string{"-ld", ".", "input.txt"}

	result, _, err := ExecuteLocally(context.Background(), command)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		if strings.HasSuffix(line, " .") {
			assert.True(t, strings.HasPrefix(line, "drwx------"), "working dir: %s", line)
		} else {
			assert.True(t, strings.HasPrefix(line, "-rw-------"), "input file: %s", line)
		}
	}
}

func Test_ExecuteLocally_subProcessHoldingOutput(t *testing.T) {
	// Without a wait delay, ExecuteLocally would wait for the
	// background sleep that inherited stdout until the context ends
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _, err := ExecuteLocally(ctx, &Command{Name: "sh", Args: []string{"-c", "sleep 30 &"}})
	require.NoError(t, err)
	assert.False(t, errors.Is(ctx.Err(), context.DeadlineExceeded))
}
