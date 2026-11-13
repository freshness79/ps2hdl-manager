//go:build windows

package tools

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console window from opening for every child process.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
