//go:build darwin

package apa

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

const (
	dkiocGetBlockSize  = 0x40046418 // DKIOCGETBLOCKSIZE
	dkiocGetBlockCount = 0x40086419 // DKIOCGETBLOCKCOUNT
)

// deviceSize returns the size of a file or /dev/diskN (0 if unknown).
func deviceSize(f *os.File) uint64 {
	var bs uint32
	var bc uint64
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), dkiocGetBlockSize, uintptr(unsafe.Pointer(&bs))); e == 0 {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), dkiocGetBlockCount, uintptr(unsafe.Pointer(&bc))); e == 0 {
			return uint64(bs) * bc
		}
	}
	n, err := f.Seek(0, io.SeekEnd)
	if err != nil || n <= 0 {
		return 0
	}
	return uint64(n)
}
