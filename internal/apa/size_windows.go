//go:build windows

package apa

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

const ioctlDiskGetLengthInfo = 0x0007405C

// deviceSize returns the size of a file or \\.\PhysicalDriveN (0 if unknown).
func deviceSize(f *os.File) uint64 {
	var length int64
	var ret uint32
	err := syscall.DeviceIoControl(syscall.Handle(f.Fd()), ioctlDiskGetLengthInfo, nil, 0,
		(*byte)(unsafe.Pointer(&length)), uint32(unsafe.Sizeof(length)), &ret, nil)
	if err == nil && length > 0 {
		return uint64(length)
	}
	n, err := f.Seek(0, io.SeekEnd)
	if err != nil || n <= 0 {
		return 0
	}
	return uint64(n)
}
