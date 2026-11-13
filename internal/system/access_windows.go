//go:build windows

package system

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func isAdmin() bool {
	t := windows.GetCurrentProcessToken()
	return t.IsElevated()
}

// isBusy: the volume is locked by Windows or another program.
func isBusy(err error) bool {
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33)) // sharing / lock violation
}
