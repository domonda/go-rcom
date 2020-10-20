package rcom

import (
	"bytes"
	"context"
	"encoding/gob"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/ungerik/go-fs"
)

func cpCommand() (command *Command, expectedFile *fs.MemFile) {
	inputFile := fs.NewMemFile("input.txt", []byte("rcom test file"))
	expectedFile = fs.NewMemFile("output.txt", []byte("rcom test file"))
	command = &Command{
		Name:               copyCmd(),
		Args:               []string{"input.txt", "output.txt"},
		Files:              map[string][]byte{inputFile.FileName: inputFile.FileData},
		ResultFilePatterns: []string{"*.txt"}, // Not necessary for the command to work, but to test the filter
	}
	return command, expectedFile
}

func Test_GobCommand(t *testing.T) {
	buf := bytes.NewBuffer(nil)

	// gob.Register(&fs.MemFile{}) // Using []fs.FileReader for Command.File does not work

	encoded, _ := cpCommand()
	err := gob.NewEncoder(buf).Encode(encoded)
	assert.NoError(t, err)

	var decoded *Command
	err = gob.NewDecoder(buf).Decode(&decoded)
	assert.NoError(t, err)

	assert.Equal(t, encoded, decoded)
}

func Test_Command_ExecuteLocally(t *testing.T) {
	command, expectedFile := cpCommand()

	result, callID, err := ExecuteLocally(context.Background(), command)
	assert.NoError(t, err)
	assert.Equal(t, result.CallID, callID, "congruent callID")

	resultFileData, ok := result.Files[expectedFile.Name()]
	assert.True(t, ok, "expected result file exists")
	assert.Equal(t, expectedFile.FileData, resultFileData, "result file has expected content")

	assert.False(t, fs.TempDir().Join(callID.String()).Exists(), "temp dir of call was removed")
}

func Test_Command_ExecuteRemotely(t *testing.T) {
	svc := &service{map[string]bool{copyCmd(): true}}
	server := httptest.NewServer(svc)
	defer server.Close()

	command, expectedFile := cpCommand()

	result, err := ExecuteRemotely(context.Background(), server.URL, command)
	assert.NoError(t, err)

	resultFileData, ok := result.Files[expectedFile.Name()]
	assert.True(t, ok, "expected result file exists")
	assert.Equal(t, expectedFile.FileData, resultFileData, "result file has expected content")
}
