//go:build !windows && !darwin

package apa

import (
	"io"
	"os"
)

// deviceSize returns the size of a file or block device (0 if unknown).
func deviceSize(f *os.File) uint64 {
	n, err := f.Seek(0, io.SeekEnd)
	if err != nil || n <= 0 {
		return 0
	}
	return uint64(n)
}
