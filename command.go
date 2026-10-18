package rcom

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// fileNameSeparators are the path separators
// that a Command file name must not contain
const fileNameSeparators = `/\`

// maxFileNameLength is the maximum file name length in bytes
// of common file systems
const maxFileNameLength = 255

// maxErrorStringLength is the maximum length in bytes
// of strings from requests that are put into error messages
const maxErrorStringLength = 256

// limitString returns s cut to maxLen bytes with "..." appended if it was longer
func limitString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

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
//   - Name is empty
//   - Any filename is empty, "." or "..", longer than 255 bytes,
//     or contains NUL or path separators, also in URL-encoded form like "%2F"
//   - Any result file pattern is empty, duplicated or malformed
func (c *Command) Validate() error {
	if c.Name == "" {
		return errors.New("rcom.Command: no command name provided")
	}
	for fileName := range c.Files {
		err := validateFileName(fileName)
		if err != nil {
			return err
		}
	}
	patterns := make(map[string]bool)
	for _, pattern := range c.ResultFilePatterns {
		if pattern == "" {
			return errors.New("rcom.Command: empty result file pattern")
		}
		if patterns[pattern] {
			return fmt.Errorf("rcom.Command: duplicate result file pattern %q", limitString(pattern, maxErrorStringLength))
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("rcom.Command: invalid result file pattern %q: %w", limitString(pattern, maxErrorStringLength), err)
		}
		patterns[pattern] = true
	}
	return nil
}

// validateFileName returns an error if fileName is not a plain file name
// that stays in its directory. The URL-decoded form of the name is checked
// too, because some file system libraries decode paths.
func validateFileName(fileName string) error {
	names := []string{fileName}
	if decoded, err := url.PathUnescape(fileName); err == nil && decoded != fileName {
		names = append(names, decoded)
	}
	for _, name := range names {
		switch {
		case name == "":
			return errors.New("rcom.Command: empty filename")
		case name == "." || name == ".." || strings.ContainsRune(name, 0) || len(name) > maxFileNameLength:
			return fmt.Errorf("rcom.Command: invalid filename %q", limitString(fileName, maxErrorStringLength))
		case strings.ContainsAny(name, fileNameSeparators):
			return fmt.Errorf("rcom.Command: filename %q must not contain path separators", limitString(fileName, maxErrorStringLength))
		}
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
