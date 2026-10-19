//go:build unix

package rcom

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ungerik/go-fs"
)

func Test_fileArg(t *testing.T) {
	for name, expected := range map[string]string{
		"input.pdf":          "./input.pdf",
		"dir/input.pdf":      "./input.pdf",
		`C:\Users\input.pdf`: "./input.pdf",
		"-o":                 "./-o",
		"--output=x":         "./--output=x",
		"/tmp/-rf":           "./-rf",
		"@options.txt":       "./@options.txt",
		"+write":             "./+write",
		"|cmd":               "./|cmd",
		"msl:x":              "./msl:x",
	} {
		assert.Equal(t, expected, fileArg(name), name)
	}
}

func Test_ClientCommand_Run(t *testing.T) {
	svc := &service{allowedCMDs: map[string]bool{"sh": true}, allowedEnvVars: []string{"OMP_THREAD_LIMIT"}}
	client := newTestClientFor(t, svc, "sh")

	// A FileReader argument is sent as input file and used by its base name
	input := fs.NewMemFile("dir/input.txt", []byte("input"))
	script := `cat "$0" > output.txt && cat extra.txt >> output.txt && cat >> output.txt && printf %s "$OMP_THREAD_LIMIT" >> output.txt; exit 3`
	result, err := client.Cmd("sh").
		WithStringArgs("-c", script).
		WithArgs(input).
		WithFiles(fs.NewMemFile("extra.txt", []byte("+extra"))).
		WithStdin([]byte("+stdin+")).
		WithEnv("OMP_THREAD_LIMIT", "1").
		WithResultFilePatterns("output.txt").
		WithNonErrorExitCodes(3).
		Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, result.ExitCode)
	output, err := result.FileData("output.txt")
	require.NoError(t, err)
	assert.Equal(t, "input+extra+stdin+1", string(output))

	// Run can be called again
	cat := client.Cmd("sh").WithStringArgs("-c", `cat "$0" extra.txt`).WithArgs(input).WithFiles(fs.NewMemFile("extra.txt", []byte("+extra")))
	for range 2 {
		result, err := cat.Run(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "input+extra", result.Stdout)
	}

	// A file named like an option is passed as a path
	option := fs.NewMemFile("-n", []byte("not an option"))
	result, err = client.Cmd("sh").WithStringArgs("-c", `printf "%s: " "$0"; cat "$0"`).WithArgs(option).Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "./-n: not an option", result.Stdout)

	_, err = client.Cmd("sh").WithArgs(42).Run(context.Background())
	assert.ErrorContains(t, err, "unsupported type int")

	_, err = client.Cmd("rm").Run(context.Background())
	assert.ErrorContains(t, err, `command "rm" not allowed`)
}
