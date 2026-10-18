package rcom

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"fmt"
	"runtime"
	"testing"

	"github.com/domonda/go-types/uu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_wireCommand(t *testing.T) {
	command := &Command{
		Name:               "convert",
		Args:               []string{"in.png", "out.jpg"},
		Stdin:              []byte("stdin"),
		Env:                map[string]string{"OMP_THREAD_LIMIT": "1", "EMPTY": "", "WITH_EQUALS": "a=b"},
		Files:              map[string][]byte{"in.png": []byte("png"), "b.txt": []byte("b"), "empty.txt": nil},
		ResultFilePatterns: []string{"*.jpg"},
		NonErrorExitCodes:  map[int]bool{1: true, 3: true},
	}
	buf := bytes.NewBuffer(nil)
	require.NoError(t, writeWireCommand(buf, command))
	decoded, err := readWireCommand(bufio.NewReader(buf))
	require.NoError(t, err)
	assert.Equal(t, command, decoded)

	// Only exit codes mapped to true are sent
	buf.Reset()
	require.NoError(t, writeWireCommand(buf, &Command{Name: "x", NonErrorExitCodes: map[int]bool{1: true, 2: false}}))
	decoded, err = readWireCommand(bufio.NewReader(buf))
	require.NoError(t, err)
	assert.Equal(t, map[int]bool{1: true}, decoded.NonErrorExitCodes)
}

func Test_wireResult(t *testing.T) {
	result := &Result{
		CallID:   uu.IDv7(),
		ExitCode: 1,
		Output:   "out err",
		Stdout:   "out",
		Stderr:   "err",
		Files:    map[string][]byte{"out.jpg": []byte("jpg"), "empty.txt": nil},
	}
	buf := bytes.NewBuffer(nil)
	require.NoError(t, writeWireResult(buf, result))
	decoded, err := readWireResult(buf)
	require.NoError(t, err)
	assert.Equal(t, result, decoded)
}

// encodeGob encodes the values as one gob stream
func encodeGob(t *testing.T, values ...any) *bytes.Buffer {
	t.Helper()
	buf := bytes.NewBuffer(nil)
	enc := gob.NewEncoder(buf)
	for _, v := range values {
		require.NoError(t, enc.Encode(v))
	}
	return buf
}

func Test_readWireCommand_limits(t *testing.T) {
	t.Run("duplicate file names", func(t *testing.T) {
		stream := encodeGob(t, &wireCommandHeader{Name: "x", NumFiles: 2}, &wireFile{Name: "a"}, &wireFile{Name: "a"})
		_, err := readWireCommand(bufio.NewReader(stream))
		assert.ErrorContains(t, err, `duplicate file name "a"`)
	})

	t.Run("duplicate environment variables", func(t *testing.T) {
		stream := encodeGob(t, &wireCommandHeader{Name: "x", Env: []string{"A=1", "A=2"}})
		_, err := readWireCommand(bufio.NewReader(stream))
		assert.ErrorContains(t, err, `duplicate environment variable "A"`)
	})

	t.Run("too many files", func(t *testing.T) {
		stream := encodeGob(t, &wireCommandHeader{Name: "x", NumFiles: maxWireFiles + 1})
		_, err := readWireCommand(bufio.NewReader(stream))
		assert.ErrorContains(t, err, fmt.Sprintf("the maximum is %d", maxWireFiles))
	})

	t.Run("maximum number of empty files", func(t *testing.T) {
		values := []any{&wireCommandHeader{Name: "x", NumFiles: maxWireFiles}}
		for i := range maxWireFiles {
			values = append(values, &wireFile{Name: fmt.Sprint(i)})
		}
		command, err := readWireCommand(bufio.NewReader(encodeGob(t, values...)))
		require.NoError(t, err)
		assert.Len(t, command.Files, maxWireFiles)
	})

	t.Run("header too large", func(t *testing.T) {
		// Every empty argument string is one byte in the message
		// but 16 bytes in memory
		stream := encodeGob(t, &wireCommandHeader{Name: "x", Args: make([]string, 2*maxWireHeaderSize)})
		allocated := measureAlloc(func() {
			_, err := readWireCommand(bufio.NewReader(stream))
			assert.ErrorIs(t, err, errWireMessageTooLarge)
		})
		assert.Less(t, allocated, uint64(8*maxWireHeaderSize), "allocated %d MiB", allocated>>20)
	})

	t.Run("declared count without data", func(t *testing.T) {
		// A message declaring 16 million exit codes but containing only one
		stream := encodeGob(t, &wireCommandHeader{Name: "x", NonErrorExitCodes: []int{123456}})
		message := setGobCount(t, stream.Bytes(), gobInt(123456), 1<<24)
		allocated := measureAlloc(func() {
			_, err := readWireCommand(bufio.NewReader(bytes.NewReader(message)))
			assert.Error(t, err, "message is truncated")
		})
		assert.Less(t, allocated, uint64(32<<20), "allocated %d MiB", allocated>>20)
	})
}

// measureAlloc returns the bytes allocated by f
func measureAlloc(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// gobInt returns the gob encoding of the int i
func gobInt(i int) []byte {
	u := uint64(i) << 1
	if i < 0 {
		u = ^u
	}
	return gobUint(u)
}

// gobUint returns the gob encoding of u: one byte below 128,
// else the negated byte count followed by big-endian bytes
func gobUint(u uint64) []byte {
	if u < 0x80 {
		return []byte{byte(u)}
	}
	var b []byte
	for ; u > 0; u >>= 8 {
		b = append([]byte{byte(u)}, b...)
	}
	return append([]byte{byte(-len(b))}, b...)
}

// setGobCount replaces the count 1 in front of the encoded element
// with count and fixes the length of the gob message containing it
func setGobCount(t *testing.T, stream, element []byte, count uint64) []byte {
	t.Helper()

	pos := bytes.Index(stream, append([]byte{1}, element...))
	require.Positive(t, pos, "count and element in stream")
	countBytes := gobUint(count)

	// Find the message containing pos by walking the length prefixes
	for start := 0; start < len(stream); {
		length, n := readGobUint(stream[start:])
		end := start + n + int(length)
		if pos < end {
			patched := append([]byte{}, stream[:start]...)
			patched = append(patched, gobUint(length+uint64(len(countBytes)-1))...)
			patched = append(patched, stream[start+n:pos]...)
			patched = append(patched, countBytes...)
			return append(patched, stream[pos+1:]...)
		}
		start = end
	}
	t.Fatal("message not found")
	return nil
}

func readGobUint(b []byte) (u uint64, n int) {
	if b[0] < 0x80 {
		return uint64(b[0]), 1
	}
	n = int(-int8(b[0]))
	for _, c := range b[1 : 1+n] {
		u = u<<8 | uint64(c)
	}
	return u, 1 + n
}
