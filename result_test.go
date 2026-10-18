package rcom

import (
	"testing"

	"github.com/domonda/go-types/uu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ungerik/go-fs"
)

func Test_Result_WriteTo(t *testing.T) {
	dir := fs.File(t.TempDir())
	result := &Result{
		CallID: uu.IDv7(),
		Files: map[string][]byte{
			"output.txt": []byte("result"),
			"empty.txt":  {},
			// An empty file decoded from gob
			"decoded-empty.txt": nil,
		},
	}

	t.Run("overwrites a longer existing file", func(t *testing.T) {
		output := dir.Join("output.txt")
		require.NoError(t, output.WriteAllString("previous, longer content"))

		require.NoError(t, result.WriteTo(output))

		data, err := output.ReadAllString()
		require.NoError(t, err)
		assert.Equal(t, "result", data, "no stale bytes of the previous content")
	})

	t.Run("missing result file", func(t *testing.T) {
		output := dir.Join("missing.txt")

		err := result.WriteTo(output)
		assert.ErrorContains(t, err, "no result file: missing.txt")
		assert.ErrorContains(t, err, result.CallID.String())
		assert.False(t, output.Exists(), "output file is not created")
	})

	t.Run("empty result file", func(t *testing.T) {
		for _, name := range []string{"empty.txt", "decoded-empty.txt"} {
			output := dir.Join(name)

			err := result.WriteTo(output)
			assert.ErrorContains(t, err, "empty result file: "+name)
			assert.False(t, output.Exists(), "output file is not created")
		}
	})
}
