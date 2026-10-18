//go:build unix

package exec

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmd_subProcessHoldingOutput(t *testing.T) {
	// Without a WaitDelay, Run would wait for the background sleep
	// that inherited stdout, and the context error would be returned
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := Command("sh", "-c", "sleep 30 & echo $!").
		WithKillSubProcesses().
		WithWaitDelay(100 * time.Millisecond).
		Run(ctx)
	require.NoError(t, err)

	// The background process left by the command is killed
	pid, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		return syscall.Kill(pid, 0) == syscall.ESRCH
	}, 5*time.Second, 10*time.Millisecond, "background process %d still running", pid)
}

func TestCmd_Run_canceledProcessEnded(t *testing.T) {
	pidFile := t.TempDir() + "/pid"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := Command("sh", "-c", `echo $$ > "$0"; exec sleep 10`, pidFile).Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The killed process has been reaped when Run returned
	pidData, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	require.NoError(t, err)
	assert.Equal(t, syscall.ESRCH, syscall.Kill(pid, 0), "process %d still exists", pid)
}
