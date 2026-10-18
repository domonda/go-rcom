package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_parseArgs(t *testing.T) {
	port, cmds, err := parseArgs([]string{"8080", "convert", "ffmpeg"}, "9000")
	require.NoError(t, err)
	assert.Equal(t, uint16(8080), port)
	assert.Equal(t, []string{"convert", "ffmpeg"}, cmds)

	port, _, err = parseArgs([]string{"", "convert"}, "9000")
	require.NoError(t, err)
	assert.Equal(t, uint16(9000), port, "PORT environment variable")

	port, _, err = parseArgs([]string{"", "convert"}, "")
	require.NoError(t, err)
	assert.Equal(t, uint16(3666), port, "default port")

	for _, args := range [][]string{nil, {"8080"}, {"70000", "convert"}, {"-1", "convert"}, {"port", "convert"}} {
		_, _, err = parseArgs(args, "")
		assert.Error(t, err, "%q", args)
	}
}

func Test_checkCommands(t *testing.T) {
	assert.NoError(t, checkCommands([]string{"go"}))
	assert.Error(t, checkCommands([]string{"go", "rcom-command-that-does-not-exist"}))
}
