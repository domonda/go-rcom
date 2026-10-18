package rcom

import (
	"fmt"
	"slices"
	"strings"

	"github.com/domonda/go-types/uu"
)

// The gob wire types use slices instead of the maps of Command and Result
// because gob allocates a map from the entry count a message declares,
// before reading any entry, so a request of a few hundred bytes could
// allocate hundreds of MiB. Gob allocates slices in chunks that grow
// with the data actually received.

type wireFile struct {
	Name string
	Data []byte
}

type wireCommand struct {
	Name               string
	Args               []string
	Stdin              []byte
	Files              []wireFile
	ResultFilePatterns []string
	NonErrorExitCodes  []int
}

type wireResult struct {
	CallID   uu.ID
	ExitCode int
	Output   string
	Stdout   string
	Stderr   string
	Files    []wireFile
}

func newWireCommand(c *Command) *wireCommand {
	w := &wireCommand{
		Name:               c.Name,
		Args:               c.Args,
		Stdin:              c.Stdin,
		Files:              newWireFiles(c.Files),
		ResultFilePatterns: c.ResultFilePatterns,
	}
	for code, nonError := range c.NonErrorExitCodes {
		if nonError {
			w.NonErrorExitCodes = append(w.NonErrorExitCodes, code)
		}
	}
	slices.Sort(w.NonErrorExitCodes)
	return w
}

func (w *wireCommand) command() (*Command, error) {
	files, err := wireFilesMap(w.Files)
	if err != nil {
		return nil, fmt.Errorf("rcom.Command: %w", err)
	}
	c := &Command{
		Name:               w.Name,
		Args:               w.Args,
		Stdin:              w.Stdin,
		Files:              files,
		ResultFilePatterns: w.ResultFilePatterns,
	}
	if len(w.NonErrorExitCodes) > 0 {
		c.NonErrorExitCodes = make(map[int]bool, len(w.NonErrorExitCodes))
		for _, code := range w.NonErrorExitCodes {
			c.NonErrorExitCodes[code] = true
		}
	}
	return c, nil
}

func newWireResult(r *Result) *wireResult {
	return &wireResult{
		CallID:   r.CallID,
		ExitCode: r.ExitCode,
		Output:   r.Output,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Files:    newWireFiles(r.Files),
	}
}

func (w *wireResult) result() (*Result, error) {
	files, err := wireFilesMap(w.Files)
	if err != nil {
		return nil, fmt.Errorf("rcom.Result: %w", err)
	}
	return &Result{
		CallID:   w.CallID,
		ExitCode: w.ExitCode,
		Output:   w.Output,
		Stdout:   w.Stdout,
		Stderr:   w.Stderr,
		Files:    files,
	}, nil
}

// newWireFiles returns the files sorted by name
func newWireFiles(files map[string][]byte) []wireFile {
	if len(files) == 0 {
		return nil
	}
	wireFiles := make([]wireFile, 0, len(files))
	for name, data := range files {
		wireFiles = append(wireFiles, wireFile{Name: name, Data: data})
	}
	slices.SortFunc(wireFiles, func(a, b wireFile) int { return strings.Compare(a.Name, b.Name) })
	return wireFiles
}

// wireFilesMap returns an error for duplicate file names,
// which a map can't represent
func wireFilesMap(wireFiles []wireFile) (map[string][]byte, error) {
	if len(wireFiles) == 0 {
		return nil, nil
	}
	files := make(map[string][]byte, len(wireFiles))
	for _, f := range wireFiles {
		if _, exists := files[f.Name]; exists {
			return nil, fmt.Errorf("duplicate file name %q", f.Name)
		}
		files[f.Name] = f.Data
	}
	return files, nil
}
