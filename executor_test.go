package rcom

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Executor(t *testing.T) {
	server := httptest.NewServer(&service{allowedCMDs: map[string]bool{copyCmd(): true}})
	defer server.Close()

	executors := map[string]Executor{
		"LocalExecutor":  LocalExecutor(),
		"RemoteExecutor": RemoteExecutor(server.URL),
	}
	for name, executor := range executors {
		t.Run(name, func(t *testing.T) {
			command, expectedFile := cpCommand()

			result, err := executor.Execute(context.Background(), command)
			require.NoError(t, err)
			assert.Equal(t, expectedFile.FileData, result.Files[expectedFile.Name()])
		})
	}
}
