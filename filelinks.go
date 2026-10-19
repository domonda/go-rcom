package rcom

// fileID identifies a file on a device
type fileID struct {
	device uint64
	index  uint64
}

// fileLinks are the identity and hard link count of a file
type fileLinks struct {
	known bool
	id    fileID
	count uint64
}
