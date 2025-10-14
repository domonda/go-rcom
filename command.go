package rcom

import (
	"errors"
	"fmt"
	"strings"
)

// Command represents a CLI command to be executed with associated input files and configuration.
// Commands can be executed locally or remotely via the rcom server.
type Command struct {
	// Name is the command to execute (e.g., "convert", "ffmpeg").
	Name string

	// Args are the command-line arguments passed to the command.
	Args []string

	// Stdin provides data to be written to the command's standard input.
	Stdin []byte

	// Files are input files to be written to the command's working directory.
	// Map keys are filenames (cannot contain path separators), values are file contents.
	Files map[string][]byte

	// ResultFilePatterns are glob patterns for selecting which files to return as results.
	// Patterns are matched against files in the command's working directory after execution.
	ResultFilePatterns []string

	// NonErrorExitCodes are non-zero exit codes that should be returned
	// as Result.ExitCode instead of being considered an error.
	// Useful for commands like grep that use non-zero codes for non-error conditions.
	NonErrorExitCodes map[int]bool

	// _ prevents unkeyed struct literals (e.g., Command{...}) forcing use of field names.
	// This makes the API more maintainable by allowing fields to be added without breaking code.
	_ struct{}
}

// Validate checks that the command is properly configured.
// Returns an error if:
// - Name is empty
// - Any filename is empty or contains path separators
// - Any result file pattern is empty or duplicated
func (c *Command) Validate() error {
	if c.Name == "" {
		return errors.New("rcom.Command: no command name provided")
	}
	for fileName := range c.Files {
		if fileName == "" {
			return errors.New("rcom.Command: empty filename")
		}
		if strings.ContainsAny(fileName, `/\`) {
			return errors.New("rcom.Command: filename must not contain path separators")
		}
	}
	patterns := make(map[string]bool)
	for _, pattern := range c.ResultFilePatterns {
		if pattern == "" {
			return errors.New("rcom.Command: empty result file pattern")
		}
		if patterns[pattern] {
			return fmt.Errorf("rcom.Command: duplicate result file pattern %q", pattern)
		}
		patterns[pattern] = true
	}
	return nil
}

// String returns a string representation of the command with its arguments.
// Implements the fmt.Stringer interface.
func (c *Command) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}
