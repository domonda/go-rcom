package rcom

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/domonda/go-rcom/pkg/exec"
	"github.com/domonda/go-types/uu"
	"github.com/ungerik/go-fs"
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
	dir := fs.TempDir().Join(callID.String())
	err = dir.MakeDir()
	if err != nil {
		return nil, callID, err
	}
	dirPath := dir.MustLocalPath()
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

	// Add files to working dir with the go-fs default
	// permissions used before for user and group read/write
	for fileName, fileData := range c.Files {
		err = root.WriteFile(fileName, fileData, 0o660)
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
		WithStdin(stdin).
		WithKillSubProcesses().
		Run(ctx)
	if err != nil {
		return nil, callID, err
	}

	if r.ExitCode != 0 && !c.NonErrorExitCodes[r.ExitCode] {
		return nil, callID, fmt.Errorf("command %q exited with code %d\nCommand output: %s", c.Name, r.ExitCode, r.Output)
	}

	files, err := readResultFiles(root, c.ResultFilePatterns)
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

// readResultFiles reads the regular files in the root directory
// with a name matching any of the patterns,
// or all regular files if no patterns are passed.
// Directories, symlinks and other special files are skipped.
func readResultFiles(root *os.Root, patterns []string) (map[string][]byte, error) {
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
		data, err := root.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		files[entry.Name()] = data
	}
	return files, nil
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
