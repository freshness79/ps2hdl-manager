//go:build windows

package system

import "os"

// detachConsole: the Windows build is a GUI-subsystem program (-H windowsgui),
// so it has no console at all; nothing to do.
func DetachConsole(*os.File) {}
