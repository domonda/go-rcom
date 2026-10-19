package rcom

import (
	"os"
	"syscall"
)

// openNonBlock is not needed on Windows,
// where named pipes are not in directories
const openNonBlock = 0

// fileLinkInfo returns the identity and hard link count of an open file
func fileLinkInfo(file *os.File) fileLinks {
	var info syscall.ByHandleFileInformation
	err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info)
	if err != nil {
		return fileLinks{}
	}
	return fileLinks{
		known: true,
		id:    fileID{device: uint64(info.VolumeSerialNumber), index: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)},
		count: uint64(info.NumberOfLinks),
	}
}
