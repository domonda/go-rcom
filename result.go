package rcom

import (
	"fmt"

	"github.com/domonda/go-types/uu"
	"github.com/ungerik/go-fs"
)

// Result contains the output and result files from command execution.
type Result struct {
	// CallID is a unique identifier for this command execution.
	// Used for logging and debugging. Generated as a UUID v7 (time-sortable).
	CallID uu.ID

	// ExitCode is the exit code returned by the command.
	ExitCode int

	// Output is the combined stdout and stderr output.
	Output string

	// Stdout is the standard output from the command.
	Stdout string

	// Stderr is the standard error output from the command.
	Stderr string

	// Files are the result files collected based on ResultFilePatterns.
	// Map keys are filenames, values are file contents.
	Files map[string][]byte

	// _ prevents unkeyed struct literals (e.g., Result{...}) forcing use of field names.
	// This makes the API more maintainable by allowing fields to be added without breaking code.
	_ struct{}
}

// WriteTo writes a result file to the specified output file.
// Returns an error if the result file doesn't exist or is empty.
// The file is identified by matching output.Name() against Files keys.
func (r *Result) WriteTo(output fs.File) error {
	rf := r.Files[output.Name()]
	switch {
	case rf == nil:
		return fmt.Errorf("no result file: %s, callID=%v", output.Name(), r.CallID)
	case len(rf) == 0:
		return fmt.Errorf("empty result file: %s, callID=%v", output.Name(), r.CallID)
	}
	return output.WriteAll(rf)
}
