//go:build unix

package rcom

import (
	"os"
	"syscall"
)

// hardLinkCount returns the number of hard links of a file
func hardLinkCount(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Nlink) //#nosec G115 -- Nlink is unsigned on every platform
	}
	return 1
}
