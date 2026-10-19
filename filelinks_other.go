//go:build !unix && !windows

package rcom

import "os"

const openNonBlock = 0

// fileLinkInfo returns unknown links on this platform
func fileLinkInfo(*os.File) fileLinks {
	return fileLinks{}
}
