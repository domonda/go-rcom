package rcom

import (
	"errors"
	"fmt"
	"strings"
)

type Command struct {
	Name               string
	Args               []string
	Stdin              []byte
	Files              map[string][]byte
	ResultFilePatterns []string
	// NonErrorExitCodes are non zero exit codes that should be returned
	// as Result.ExitCode instead of being considered an error
	NonErrorExitCodes map[int]bool
	_                 struct{}
}

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

func (c *Command) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}
