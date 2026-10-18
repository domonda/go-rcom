package exec

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmd_Run_output(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}

	// Write to stdout and stderr interleaved so that
	// both copying goroutines write to the combined output
	script := `i=0; while [ $i -lt 2000 ]; do echo out; echo err >&2; i=$((i+1)); done; exit 3`
	result, err := Command("sh", "-c", script).Run(context.Background())
	require.NoError(t, err, "non-zero exit code is not an error")

	assert.Equal(t, 3, result.ExitCode)
	assert.Equal(t, strings.Repeat("out\n", 2000), result.Stdout)
	assert.Equal(t, strings.Repeat("err\n", 2000), result.Stderr)
	assert.Len(t, result.Output, len(result.Stdout)+len(result.Stderr), "combined output has every byte")
	assert.Error(t, result.CheckExitCode(0))
	assert.NoError(t, result.CheckExitCode(0, 3))
}

func TestCmd_Run_contextCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := Command("sleep", "10").WithKillSubProcesses().Run(ctx)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "error %v wraps the context error", err)

	_, err = Command("sleep", "10").Run(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "an already canceled context is returned")
}

func TestCmd_WithEnvVar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}

	result, err := Command("sh", "-c", `printf %s "$RCOM_TEST" && pwd`).
		WithEnvVar("RCOM_TEST", "value").
		WithDir(t.TempDir()).
		WithStdin(strings.NewReader("")).
		Run(context.Background())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(result.Stdout, "value"), "stdout %q", result.Stdout)
}
