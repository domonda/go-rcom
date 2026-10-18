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
	"sync"
	"syscall"
	"time"
)

// ErrOutputTooLarge is returned by Cmd.Run if the command wrote
// more output than the limit set with Cmd.WithMaxOutputSize
var ErrOutputTooLarge = errors.New("command output too large")

type Cmd struct {
	cmd           *exec.Cmd
	killSubProc   bool
	maxOutputSize int64
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

// WithKillSubProcesses kills the subprocesses of the command
// when the context of Run is canceled and when the command has exited.
// Not supported on Windows, where only the command process is killed.
func (c *Cmd) WithKillSubProcesses() *Cmd {
	c.killSubProc = true
	return c
}

// WithWaitDelay bounds how long Run waits for the output pipes
// to be closed after the process exited, which a subprocess
// that inherited them can delay. See os/exec.Cmd.WaitDelay.
func (c *Cmd) WithWaitDelay(delay time.Duration) *Cmd {
	c.cmd.WaitDelay = delay
	return c
}

// WithMaxOutputSize makes Run kill the command and return ErrOutputTooLarge
// if it writes more than maxSize bytes to stdout and stderr together.
// Zero or a negative maxSize means no limit.
func (c *Cmd) WithMaxOutputSize(maxSize int64) *Cmd {
	c.maxOutputSize = maxSize
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
	// os/exec copies stdout and stderr in separate goroutines,
	// so their writes to combined have to be serialized.
	// combinedWriter comes first to limit stdout and stderr too.
	combinedWriter := &lockedWriter{w: &combined, maxSize: c.maxOutputSize, onExceeded: func() { _ = c.kill() }}
	c.cmd.Stdout = io.MultiWriter(combinedWriter, &stdout)
	c.cmd.Stderr = io.MultiWriter(combinedWriter, &stderr)

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
		c.killSubProcesses()
		var exitError *exec.ExitError
		if errors.As(err, &exitError) || errors.Is(err, exec.ErrWaitDelay) {
			// We are not interested in exit errors
			// because they are just a wrapper for
			// non zero exit status codes
			// which we also get from c.cmd.ProcessState,
			// and the process has exited if a subprocess
			// held the output pipes open after the WaitDelay.
			err = nil
		}
		if combinedWriter.exceeded() {
			err = fmt.Errorf("%w: more than %d bytes", ErrOutputTooLarge, c.maxOutputSize)
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

// lockedWriter serializes writes to w and, if maxSize is greater than zero,
// returns ErrOutputTooLarge after maxSize bytes and calls onExceeded once
type lockedWriter struct {
	mu         sync.Mutex
	w          io.Writer
	written    int64
	maxSize    int64
	onExceeded func()
	isExceeded bool
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.maxSize > 0 && l.written+int64(len(p)) > l.maxSize {
		if !l.isExceeded {
			l.isExceeded = true
			l.onExceeded()
		}
		return 0, ErrOutputTooLarge
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	return n, err
}

func (l *lockedWriter) exceeded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.isExceeded
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
