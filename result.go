package rcom

import (
	"context"
	"fmt"

	"github.com/domonda/go-types/uu"
	"github.com/ungerik/go-fs"
)

type Result struct {
	CallID   uu.ID
	ExitCode int
	Output   string
	Stdout   string
	Stderr   string
	Files    map[string][]byte
	_        struct{}
}

func (r *Result) WriteTo(output fs.File) error {
	rf := r.Files[output.Name()]
	switch {
	case rf == nil:
		return fmt.Errorf("no result file: %s, callID=%v", output.Name(), r.CallID)
	case len(rf) == 0:
		return fmt.Errorf("empty result file: %s, callID=%v", output.Name(), r.CallID)
	}
	return output.WriteAll(context.Background(), rf)
}
