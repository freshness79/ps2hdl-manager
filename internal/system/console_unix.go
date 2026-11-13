//go:build !windows

package system

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// detachConsole makes the app independent of the terminal it may have been
// started from: stdin reads nothing, stdout/stderr (Go runtime, GUI toolkit,
// C libraries) go to the log file, and closing the terminal does not quit it.
func DetachConsole(logF *os.File) {
	if null, err := os.Open(os.DevNull); err == nil {
		_ = unix.Dup2(int(null.Fd()), 0)
		null.Close()
	}
	out := logF
	if out == nil {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		out = f
	}
	_ = unix.Dup2(int(out.Fd()), 1)
	_ = unix.Dup2(int(out.Fd()), 2)
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
}
