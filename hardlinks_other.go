//go:build !unix

package rcom

import "os"

// hardLinkCount returns 1 because the number of hard links
// is not available from os.FileInfo on this platform
func hardLinkCount(os.FileInfo) uint64 {
	return 1
}
