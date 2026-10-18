package rcom

import (
	"bytes"
	"encoding/gob"
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
		Files:              map[string][]byte{"in.png": []byte("png"), "b.txt": []byte("b"), "empty.txt": {}},
		ResultFilePatterns: []string{"*.jpg"},
		NonErrorExitCodes:  map[int]bool{1: true, 3: true, 2: false},
	}
	wire := newWireCommand(command)
	assert.Equal(t, []string{"b.txt", "empty.txt", "in.png"}, []string{wire.Files[0].Name, wire.Files[1].Name, wire.Files[2].Name}, "files sorted by name")
	assert.Equal(t, []int{1, 3}, wire.NonErrorExitCodes, "only codes mapped to true")

	buf := bytes.NewBuffer(nil)
	require.NoError(t, gob.NewEncoder(buf).Encode(wire))
	var decodedWire wireCommand
	require.NoError(t, gob.NewDecoder(buf).Decode(&decodedWire))
	decoded, err := decodedWire.command()
	require.NoError(t, err)

	assert.Equal(t, command.Name, decoded.Name)
	assert.Equal(t, command.Args, decoded.Args)
	assert.Equal(t, command.Stdin, decoded.Stdin)
	assert.Equal(t, command.ResultFilePatterns, decoded.ResultFilePatterns)
	assert.Equal(t, map[int]bool{1: true, 3: true}, decoded.NonErrorExitCodes)
	assert.Equal(t, []byte("png"), decoded.Files["in.png"])
	assert.Contains(t, decoded.Files, "empty.txt", "empty file is kept")

	_, err = (&wireCommand{Name: "x", Files: []wireFile{{Name: "a"}, {Name: "a"}}}).command()
	assert.ErrorContains(t, err, `duplicate file name "a"`)
}

func Test_wireResult(t *testing.T) {
	result := &Result{
		CallID:   uu.IDv7(),
		ExitCode: 1,
		Output:   "out err",
		Stdout:   "out",
		Stderr:   "err",
		Files:    map[string][]byte{"out.jpg": []byte("jpg")},
	}
	buf := bytes.NewBuffer(nil)
	require.NoError(t, gob.NewEncoder(buf).Encode(newWireResult(result)))
	var wire wireResult
	require.NoError(t, gob.NewDecoder(buf).Decode(&wire))
	decoded, err := wire.result()
	require.NoError(t, err)
	assert.Equal(t, result, decoded)

	_, err = (&wireResult{Files: []wireFile{{Name: "a"}, {Name: "a"}}}).result()
	assert.ErrorContains(t, err, `duplicate file name "a"`)
}

// Test_wireCommand_declaredCount decodes a message that declares
// 16 million exit codes but contains only one, which must not
// allocate memory for the declared count up front.
func Test_wireCommand_declaredCount(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	require.NoError(t, gob.NewEncoder(buf).Encode(newWireCommand(&Command{
		Name:              "x",
		NonErrorExitCodes: map[int]bool{123456: true},
	})))
	message := setGobCount(t, buf.Bytes(), gobInt(123456), 1<<24)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var wire wireCommand
	err := gob.NewDecoder(bytes.NewReader(message)).Decode(&wire)
	runtime.ReadMemStats(&after)

	assert.Error(t, err, "message is truncated")
	allocated := after.TotalAlloc - before.TotalAlloc
	assert.Less(t, allocated, uint64(32<<20), "allocated %d MiB", allocated>>20)
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
