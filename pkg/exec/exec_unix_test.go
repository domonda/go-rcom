//go:build unix

package exec

import (
	"context"
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
