package rcom

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/domonda/go-rcom/pkg/exec"
	"github.com/domonda/go-types/uu"
)

// ExecuteLocally executes a command locally in an isolated temporary directory.
//
// The function:
//  1. Creates a unique temporary directory named with a UUID v7
//  2. Writes all input files to the temporary directory
//  3. Executes the command with the temp directory as working directory
//  4. Provides stdin data if specified
//  5. Collects stdout, stderr, and exit code
//  6. Reads result files matching ResultFilePatterns
//  7. Cleans up the temporary directory
//
// Returns:
//   - result: The command result including output and files
//   - callID: Unique UUID v7 identifier for this execution
//   - err: Any error that occurred during execution
//
// Non-zero exit codes are treated as errors unless listed in Command.NonErrorExitCodes.
// The command respects context cancellation and will terminate child processes.
func ExecuteLocally(ctx context.Context, c *Command) (result *Result, callID uu.ID, err error) {
	start := time.Now()
	// Every call gets a UUID
	callID = uu.IDv7()

	log := log.With().
		UUID("callID", callID).
		Str("command", c.Name).
		SubLogger()

	log.Debug("ExecuteLocally").Log()

	err = c.Validate()
	if err != nil {
		return nil, callID, err
	}

	// Create unique temp working directory for call
	// that only the user of the process can access
	dirPath := filepath.Join(os.TempDir(), callID.String())
	err = os.Mkdir(dirPath, 0o700)
	if err != nil {
		return nil, callID, err
	}
	// Clean up after call and log errors.
	// os.RemoveAll removes symlinks the command created
	// without deleting what they point to.
	defer func() {
		err := os.RemoveAll(dirPath)
		if err != nil {
			log.Error("Can't clean up after command").Err(err).Log()
		}
	}()

	// All file access goes through root so that no file name
	// or symlink can reach outside of the working dir
	root, err := os.OpenRoot(dirPath)
	if err != nil {
		return nil, callID, err
	}
	defer root.Close()

	// Add files to working dir
	for fileName, fileData := range c.Files {
		err = writeNewFile(root, fileName, fileData)
		if err != nil {
			return nil, callID, fmt.Errorf("can't copy file %q because of error: %w", fileName, err)
		}
	}

	var stdin io.Reader
	if len(c.Stdin) > 0 {
		stdin = bytes.NewReader(c.Stdin)
	}

	r, err := exec.Command(c.Name, c.Args...).
		WithDir(dirPath).
		WithEnv(commandEnv()).
		WithStdin(stdin).
		WithKillSubProcesses().
		WithWaitDelay(commandWaitDelay).
		WithMaxOutputSize(MaxOutputSize).
		Run(ctx)
	if err != nil {
		return nil, callID, err
	}

	if r.ExitCode != 0 && !c.NonErrorExitCodes[r.ExitCode] {
		return nil, callID, fmt.Errorf("command %q exited with code %d\nCommand output: %s", c.Name, r.ExitCode, outputTail(r.Output))
	}

	files, err := readResultFiles(root, c.ResultFilePatterns, MaxResultFilesSize)
	if err != nil {
		return nil, callID, fmt.Errorf("can't read result files: %w", err)
	}

	result = &Result{
		CallID:   callID,
		ExitCode: r.ExitCode,
		Output:   r.Output,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Files:    files,
	}

	log.Debug("ExecuteLocally finished").
		Stringer("duration", time.Since(start)).
		Log()

	return result, callID, nil
}

const (
	// commandWaitDelay is how long ExecuteLocally waits for the output
	// of a command after it exited, which subprocesses can keep open
	commandWaitDelay = 2 * time.Second

	// maxErrorOutputSize is how many bytes of the output
	// of a failed command its error contains
	maxErrorOutputSize = 64 << 10
)

// commandEnv returns the process environment with only the InheritedEnvVars,
// or nil to inherit all variables if InheritedEnvVars is nil
func commandEnv() []string {
	if InheritedEnvVars == nil {
		return nil
	}
	env := []string{} // Not nil, which would inherit all variables
	for _, nameValue := range os.Environ() {
		name, _, _ := strings.Cut(nameValue, "=")
		inherited := slices.ContainsFunc(InheritedEnvVars, func(inherited string) bool {
			if runtime.GOOS == "windows" {
				return strings.EqualFold(inherited, name)
			}
			return inherited == name
		})
		if inherited {
			env = append(env, nameValue)
		}
	}
	return env
}

// outputTail returns the last maxErrorOutputSize bytes of output
func outputTail(output string) string {
	if len(output) <= maxErrorOutputSize {
		return output
	}
	return fmt.Sprintf("[%d bytes omitted]...%s", len(output)-maxErrorOutputSize, output[len(output)-maxErrorOutputSize:])
}

// writeNewFile writes data to a new file in root that only the user
// of the process can access. It returns an error if the file already
// exists, which happens for names that differ only in case
// on case-insensitive file systems.
func writeNewFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// readResultFiles reads the regular files in the root directory
// with a name matching any of the patterns,
// or all regular files if no patterns are passed.
// Directories, symlinks, other special files and files with more
// than one hard link, which could be a file outside of root,
// are skipped. An error is returned if the files are larger than
// maxSize bytes together and maxSize is greater than zero.
func readResultFiles(root *os.Root, patterns []string, maxSize int64) (map[string][]byte, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	files := make(map[string][]byte)
	var size int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		match, err := matchAnyPattern(entry.Name(), patterns)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if hardLinkCount(info) > 1 {
			continue
		}
		limit := int64(-1)
		if maxSize > 0 {
			limit = maxSize - size
		}
		data, err := readFileLimited(root, entry.Name(), limit)
		if err != nil {
			return nil, err
		}
		if data == nil {
			return nil, fmt.Errorf("result files larger than %d bytes", maxSize)
		}
		size += int64(len(data))
		files[entry.Name()] = data
	}
	return files, nil
}

// readFileLimited reads the named file in root and returns nil data
// if it is larger than limit, unless limit is negative
func readFileLimited(root *os.Root, name string, limit int64) ([]byte, error) {
	if limit < 0 {
		return root.ReadFile(name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, nil
	}
	return data, nil
}

func matchAnyPattern(name string, patterns []string) (bool, error) {
	if len(patterns) == 0 {
		return true, nil
	}
	for _, pattern := range patterns {
		match, err := path.Match(pattern, name)
		if match || err != nil {
			return match, err
		}
	}
	return false, nil
}
