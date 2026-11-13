//go:build !windows

package system

import (
	"errors"
	"os"
	"syscall"
)

func isAdmin() bool { return os.Geteuid() == 0 }

func isBusy(err error) bool { return errors.Is(err, syscall.EBUSY) }
