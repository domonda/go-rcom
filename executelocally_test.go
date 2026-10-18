package rcom

import (
	"context"
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
