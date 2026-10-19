package rcom

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_SetLogger_nil(t *testing.T) {
	defaultLogger := log
	t.Cleanup(func() { SetLogger(defaultLogger) })

	// A nil logger disables logging in the client, the server
	// and the local execution that the server runs.
	SetLogger(nil)

	server := httptest.NewServer(&service{allowedCMDs: map[string]bool{copyCmd(): true}})
	defer server.Close()

	command, expectedFile := cpCommand()
	result, err := ExecuteRemotely(context.Background(), server.URL, command)
	require.NoError(t, err)
	assert.Equal(t, expectedFile.FileData, result.Files[expectedFile.Name()])
}

// setVar sets a package variable for the duration of the test
func setVar[T any](t *testing.T, variable *T, value T) {
	t.Helper()
	previous := *variable
	*variable = value
	t.Cleanup(func() { *variable = previous })
}
