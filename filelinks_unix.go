//go:build unix

package rcom

import (
	"os"
	"syscall"
)

// openNonBlock makes opening a FIFO return immediately
const openNonBlock = syscall.O_NONBLOCK

// fileLinkInfo returns the identity and hard link count of an open file
func fileLinkInfo(file *os.File) fileLinks {
	info, err := file.Stat()
	if err != nil {
		return fileLinks{}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileLinks{}
	}
	return fileLinks{
		known: true,
		id:    fileID{device: uint64(stat.Dev), index: uint64(stat.Ino)}, //#nosec G115 -- identifiers, not quantities
		count: uint64(stat.Nlink),                                        //#nosec G115 -- never negative
	}
}
