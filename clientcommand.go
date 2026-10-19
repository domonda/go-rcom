package rcom

import (
	"context"
	"fmt"
	"slices"

	"github.com/ungerik/go-fs"
)

// Cmd returns a ClientCommand for the allowed command name,
// which is configured with its With methods and executed with Run:
//
//	result, err := client.Cmd("pdftotext").
//		WithArgs("-layout", pdfFile, "output.txt").
//		WithResultFilePatterns("output.txt").
//		Run(ctx)
func (c *Client) Cmd(name string) *ClientCommand {
	return &ClientCommand{client: c, command: Command{Name: name}}
}

// ClientCommand is a command of a Client
// configured with its With methods and executed with Run.
type ClientCommand struct {
	client  *Client
	command Command
	args    []any
	files   []fs.FileReader
}

// WithArgs appends arguments, which can be strings or fs.FileReader
// like fs.File or fs.MemFile. A FileReader is sent as input file
// and its base name prefixed with "./" is used as the argument,
// so that its name is not an option of the command.
func (cc *ClientCommand) WithArgs(args ...any) *ClientCommand {
	cc.args = append(cc.args, args...)
	return cc
}

// WithStringArgs appends string arguments.
func (cc *ClientCommand) WithStringArgs(args ...string) *ClientCommand {
	for _, arg := range args {
		cc.args = append(cc.args, arg)
	}
	return cc
}

// WithFiles adds input files that are sent under their base name.
// Arguments naming them must use the base name too, which differs
// from the file's Name for a path like the Windows path of a browser
// upload, or pass the file to WithArgs, which does that.
func (cc *ClientCommand) WithFiles(files ...fs.FileReader) *ClientCommand {
	cc.files = append(cc.files, files...)
	return cc
}

// WithStdin sets the standard input of the command.
func (cc *ClientCommand) WithStdin(stdin []byte) *ClientCommand {
	cc.command.Stdin = stdin
	return cc
}

// WithEnv sets an environment variable for the command,
// which the server must allow with AllowedCommandEnvVars.
func (cc *ClientCommand) WithEnv(name, value string) *ClientCommand {
	if cc.command.Env == nil {
		cc.command.Env = make(map[string]string)
	}
	cc.command.Env[name] = value
	return cc
}

// WithResultFilePatterns appends glob patterns of the result files.
func (cc *ClientCommand) WithResultFilePatterns(patterns ...string) *ClientCommand {
	cc.command.ResultFilePatterns = append(cc.command.ResultFilePatterns, patterns...)
	return cc
}

// WithNonErrorExitCodes adds non-zero exit codes
// that are returned as Result.ExitCode instead of an error.
func (cc *ClientCommand) WithNonErrorExitCodes(exitCodes ...int) *ClientCommand {
	if cc.command.NonErrorExitCodes == nil {
		cc.command.NonErrorExitCodes = make(map[int]bool)
	}
	for _, code := range exitCodes {
		cc.command.NonErrorExitCodes[code] = true
	}
	return cc
}

// Run executes the command on the server of the client
// with the timeout and retries of the client.
func (cc *ClientCommand) Run(ctx context.Context) (*Result, error) {
	command := cc.command
	// Clipped so that appending does not write to cc.files
	files := slices.Clip(cc.files)
	for i, arg := range cc.args {
		switch arg := arg.(type) {
		case string:
			command.Args = append(command.Args, arg)
		case fs.FileReader:
			command.Args = append(command.Args, fileArg(arg.Name()))
			files = append(files, arg)
		default:
			return nil, fmt.Errorf("rcom.ClientCommand: argument %d has unsupported type %T, use string or fs.FileReader", i, arg)
		}
	}
	var err error
	command.Files, err = readInputFiles(ctx, files)
	if err != nil {
		return nil, err
	}
	return cc.client.run(ctx, &command)
}

// fileArg returns the argument for an input file named name:
// its base name prefixed with "./", so that a name chosen by the
// uploader of a file like "-o", "@options" or "|cmd" is not
// an option or special syntax of the command
func fileArg(name string) string {
	return "./" + baseName(name)
}
