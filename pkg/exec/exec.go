/*
Package exec is a replacement for os/exec and
provides a Cmd struct that wraps os/exec.Cmd
with additional builder design pattern methods
and an option to kill sub processes.
*/
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
)

type Cmd struct {
	cmd         *exec.Cmd
	killSubProc bool
}

// Command returns the Cmd struct to execute the named program with
// the given arguments.
//
// If name contains no path separators, Command uses LookPath to
// resolve name to a complete path if possible. Otherwise it uses name
// directly as Path.
//
// The returned Cmd's arguments are constructed from the command name
// followed by the elements of arg, so arg should not include the
// command name itself. For example, Command("echo", "hello").
func Command(name string, args ...string) *Cmd {
	return &Cmd{cmd: exec.Command(name, args...)}
}

// WithDir specifies the working directory of the command.
// If dir is the empty string, Run runs the command in the
// calling process's current directory.
func (c *Cmd) WithDir(dir string) *Cmd {
	c.cmd.Dir = dir
	return c
}

// WithEnv specifies the environment of the process.
// Each entry is of the form "key=value".
// If env is nil, the new process uses the current process's
// environment.
// If env contains duplicate environment keys, only the last
// value in the slice for each duplicate key is used.
// As a special case on Windows, SYSTEMROOT is always added if
// missing and not explicitly set to the empty string.
func (c *Cmd) WithEnv(env []string) *Cmd {
	c.cmd.Env = env
	return c
}

// WithEnvVar inherits the current process's environment
// and adds another variable named key with the passed value.
func (c *Cmd) WithEnvVar(key, value string) *Cmd {
	if c.cmd.Env == nil {
		c.cmd.Env = syscall.Environ()
	}
	c.cmd.Env = append(c.cmd.Env, key+"="+value)
	return c
}

// WithStdin specifies the process's standard input.
//
// If stdin is nil, the process reads from the null device (os.DevNull).
//
// If stdin is an *os.File, the process's standard input is connected
// directly to that file.
//
// Otherwise, during the execution of the command a separate
// goroutine reads from stdin and delivers that data to the command
// over a pipe. In this case, Wait does not complete until the goroutine
// stops copying, either because it has reached the end of stdin
// (EOF or a read error) or because writing to the pipe returned an error.
func (c *Cmd) WithStdin(stdin io.Reader) *Cmd {
	c.cmd.Stdin = stdin
	return c
}

func (c *Cmd) WithKillSubProcesses() *Cmd {
	c.killSubProc = true
	return c
}

// String returns a string representation of the command
// which might not exactly be identical to the real
// command line that would be exected.
func (c *Cmd) String() string {
	return c.cmd.String()
}

// Run the command and wait for the process to exit or the context to be canceled.
// Non zero exit codes are not cosidered errors.
// Context errors will be returned in wrapped form.
func (c *Cmd) Run(ctx context.Context) (*Result, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	c.setSysProcAttr()

	var (
		stdout   strings.Builder
		stderr   strings.Builder
		combined strings.Builder
	)
	c.cmd.Stdout = io.MultiWriter(&stdout, &combined)
	c.cmd.Stderr = io.MultiWriter(&stderr, &combined)

	err := c.cmd.Start()
	if err != nil {
		return nil, err
	}

	done := make(chan error, 2)
	dontKill := make(chan struct{}, 1)

	go func() {
		select {
		case <-ctx.Done():
			done <- c.kill()
		case <-dontKill:
			return
		}
	}()

	go func() {
		err := c.cmd.Wait()
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			// We are not interested in exit errors
			// because they are just a wrapper for
			// non zero exit status codes
			// which we also get from c.cmd.ProcessState.
			err = nil
		}
		dontKill <- struct{}{}
		done <- err
	}()

	// Wait for either finished command run or canceled context
	err = <-done

	switch {
	case ctx.Err() != nil && err != nil:
		return nil, fmt.Errorf("%s error killing process because of: %w", err, ctx.Err())
	case ctx.Err() != nil:
		return nil, fmt.Errorf("killed process because of: %w", ctx.Err())
	case err != nil:
		return nil, err
	}

	result := &Result{
		ExitCode:  c.cmd.ProcessState.ExitCode(),
		ExitState: c.cmd.ProcessState.String(),
		Output:    combined.String(),
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
	}

	return result, nil
}

type Result struct {
	ExitCode  int
	ExitState string
	Output    string
	Stdout    string
	Stderr    string
}

// CheckExitCode returns the ExitState as error if
// the ExitCode is not one of the passed validExitCodes
// or non zero if no validExitCodes are passed.
func (r *Result) CheckExitCode(validExitCodes ...int) error {
	if len(validExitCodes) == 0 && r.ExitCode == 0 {
		return nil
	}
	for _, validCode := range validExitCodes {
		if r.ExitCode == validCode {
			return nil
		}
	}
	return errors.New(r.ExitState)
}
