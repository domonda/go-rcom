package rcom

import (
	"bufio"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/domonda/go-types/uu"
)

// Commands and results are sent as a stream of gob messages:
// a header, for a command its stdin, and then one message per file.
//
// Gob allocates decoded values ahead of the data: a map from the entry
// count a message declares, a slice element of up to 40 bytes for every
// byte of an empty element. A request of a few bytes per element could
// make the server allocate 40 to 80 times its size, or hundreds of MiB
// with maps. So the header, which holds the slices, is limited to
// maxWireHeaderSize, and every file is its own message.

const (
	// maxWireHeaderSize is the maximum size of a command header message.
	// Gob decodes an empty string of one byte into 16 bytes,
	// so the decoded header takes at most about 5 MiB.
	maxWireHeaderSize = 256 << 10

	// maxWireFiles is the maximum number of files of a command or result
	maxWireFiles = 10000
)

var errWireMessageTooLarge = errors.New("rcom: message too large")

type wireCommandHeader struct {
	Name               string
	Args               []string
	Env                []string // NAME=value
	ResultFilePatterns []string
	NonErrorExitCodes  []int
	HasStdin           bool
	NumFiles           int
}

type wireStdin struct {
	Data []byte
}

type wireFile struct {
	Name string
	Data []byte
}

type wireResultHeader struct {
	CallID   uu.ID
	ExitCode int
	Output   string
	Stdout   string
	Stderr   string
	NumFiles int
}

// writeWireCommand writes the command as a gob stream to w
func writeWireCommand(w io.Writer, c *Command) error {
	header := wireCommandHeader{
		Name:               c.Name,
		Args:               c.Args,
		ResultFilePatterns: c.ResultFilePatterns,
		HasStdin:           len(c.Stdin) > 0,
		NumFiles:           len(c.Files),
	}
	for code, nonError := range c.NonErrorExitCodes {
		if nonError {
			header.NonErrorExitCodes = append(header.NonErrorExitCodes, code)
		}
	}
	slices.Sort(header.NonErrorExitCodes)
	for name, value := range c.Env {
		header.Env = append(header.Env, name+"="+value)
	}
	slices.Sort(header.Env)

	enc := gob.NewEncoder(w)
	err := enc.Encode(&header)
	if err != nil {
		return err
	}
	if header.HasStdin {
		err = enc.Encode(&wireStdin{Data: c.Stdin})
		if err != nil {
			return err
		}
	}
	return writeWireFiles(enc, c.Files)
}

// readWireCommand reads a command written by writeWireCommand from r.
// The caller limits the total size of the stream.
func readWireCommand(r *bufio.Reader) (*Command, error) {
	budget := &budgetReader{r: r, n: maxWireHeaderSize}
	dec := gob.NewDecoder(budget)
	var header wireCommandHeader
	err := dec.Decode(&header)
	if err != nil {
		return nil, err
	}
	if header.NumFiles < 0 || header.NumFiles > maxWireFiles {
		return nil, fmt.Errorf("rcom.Command: %d files, the maximum is %d", header.NumFiles, maxWireFiles)
	}
	c := &Command{
		Name:               header.Name,
		Args:               header.Args,
		ResultFilePatterns: header.ResultFilePatterns,
	}
	if len(header.NonErrorExitCodes) > 0 {
		c.NonErrorExitCodes = make(map[int]bool, len(header.NonErrorExitCodes))
		for _, code := range header.NonErrorExitCodes {
			c.NonErrorExitCodes[code] = true
		}
	}
	if len(header.Env) > 0 {
		c.Env = make(map[string]string, len(header.Env))
		for _, nameValue := range header.Env {
			name, value, _ := strings.Cut(nameValue, "=")
			if _, exists := c.Env[name]; exists {
				return nil, fmt.Errorf("rcom.Command: duplicate environment variable %q", limitString(name, maxErrorStringLength))
			}
			c.Env[name] = value
		}
	}

	// Stdin and files are allocated as their data arrives
	budget.n = math.MaxInt64
	if header.HasStdin {
		var stdin wireStdin
		err = dec.Decode(&stdin)
		if err != nil {
			return nil, err
		}
		c.Stdin = stdin.Data
	}
	c.Files, err = readWireFiles(dec, header.NumFiles)
	if err != nil {
		return nil, fmt.Errorf("rcom.Command: %w", err)
	}
	return c, nil
}

// writeWireResult writes the result as a gob stream to w
func writeWireResult(w io.Writer, r *Result) error {
	enc := gob.NewEncoder(w)
	err := enc.Encode(&wireResultHeader{
		CallID:   r.CallID,
		ExitCode: r.ExitCode,
		Output:   r.Output,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		NumFiles: len(r.Files),
	})
	if err != nil {
		return err
	}
	return writeWireFiles(enc, r.Files)
}

// readWireResult reads a result written by writeWireResult from r
func readWireResult(r io.Reader) (*Result, error) {
	dec := gob.NewDecoder(r)
	var header wireResultHeader
	err := dec.Decode(&header)
	if err != nil {
		return nil, err
	}
	if header.NumFiles < 0 || header.NumFiles > maxWireFiles {
		return nil, fmt.Errorf("rcom.Result: %d files, the maximum is %d", header.NumFiles, maxWireFiles)
	}
	files, err := readWireFiles(dec, header.NumFiles)
	if err != nil {
		return nil, fmt.Errorf("rcom.Result: %w", err)
	}
	return &Result{
		CallID:   header.CallID,
		ExitCode: header.ExitCode,
		Output:   header.Output,
		Stdout:   header.Stdout,
		Stderr:   header.Stderr,
		Files:    files,
	}, nil
}

// writeWireFiles writes the files sorted by name
func writeWireFiles(enc *gob.Encoder, files map[string][]byte) error {
	for _, name := range slices.Sorted(maps.Keys(files)) {
		err := enc.Encode(&wireFile{Name: name, Data: files[name]})
		if err != nil {
			return err
		}
	}
	return nil
}

// readWireFiles returns an error for duplicate file names,
// which a map can't represent
func readWireFiles(dec *gob.Decoder, numFiles int) (map[string][]byte, error) {
	if numFiles == 0 {
		return nil, nil
	}
	files := make(map[string][]byte, numFiles)
	for range numFiles {
		var file wireFile
		err := dec.Decode(&file)
		if err != nil {
			return nil, err
		}
		if _, exists := files[file.Name]; exists {
			return nil, fmt.Errorf("duplicate file name %q", limitString(file.Name, maxErrorStringLength))
		}
		files[file.Name] = file.Data
	}
	return files, nil
}

// budgetReader reads at most n bytes from r and then returns
// errWireMessageTooLarge. It implements io.ByteReader so that
// gob reads exactly its messages from it without buffering.
type budgetReader struct {
	r *bufio.Reader
	n int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.n <= 0 {
		return 0, errWireMessageTooLarge
	}
	if int64(len(p)) > b.n {
		p = p[:b.n]
	}
	n, err := b.r.Read(p)
	b.n -= int64(n)
	return n, err
}

func (b *budgetReader) ReadByte() (byte, error) {
	if b.n <= 0 {
		return 0, errWireMessageTooLarge
	}
	c, err := b.r.ReadByte()
	if err == nil {
		b.n--
	}
	return c, err
}
