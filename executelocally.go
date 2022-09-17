package rcom

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/domonda/go-rcom/pkg/exec"
	"github.com/domonda/go-types/uu"
	"github.com/ungerik/go-fs"
)

func ExecuteLocally(ctx context.Context, c *Command) (result *Result, callID uu.ID, err error) {
	start := time.Now()
	// Every call gets a UUID
	callID = uu.IDv4()

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
	// Clean up after call and log errors
	defer func() {
		err := dir.RemoveRecursive()
		if err != nil {
			log.Error("Can't clean up after command").Err(err).Log()
		}
	}()

	// Add files to working dir
	for fileName, fileData := range c.Files {
		err = dir.Join(fileName).WriteAll(ctx, fileData)
		if err != nil {
			return nil, callID, fmt.Errorf("can't copy file %q because of error: %w", fileName, err)
		}
	}

	var stdin io.Reader
	if len(c.Stdin) > 0 {
		stdin = bytes.NewReader(c.Stdin)
	}

	r, err := exec.Command(c.Name, c.Args...).
		WithDir(dir.MustLocalPath()).
		WithStdin(stdin).
		WithKillSubProcesses().
		Run(ctx)
	if err != nil {
		return nil, callID, err
	}

	if r.ExitCode != 0 && (c.NonErrorExitCodes != nil || c.NonErrorExitCodes[r.ExitCode] == false) {
		return nil, callID, fmt.Errorf("%w\nCommand output: %s", err, r.Output)
	}

	result = &Result{
		CallID:   callID,
		ExitCode: r.ExitCode,
		Output:   r.Output,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Files:    make(map[string][]byte),
	}

	err = dir.ListDir(
		func(file fs.File) error {
			if file.IsDir() {
				return nil
			}
			data, err := file.ReadAll(ctx)
			if err != nil {
				return err
			}
			result.Files[file.Name()] = data
			return nil
		},
		c.ResultFilePatterns...,
	)

	log.Debug("ExecuteLocally finished").
		Stringer("duration", time.Since(start)).
		Log()

	return result, callID, nil
}
