// Command ps2hdl-manager is PS2 HDL Manager, a graphical manager for PlayStation 2
// hard drives (HD Loader / Open PS2 Loader).
//
// It has no command-line options: it always opens the user interface.
package main

import (
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"github.com/freshness79/ps2hdl-manager/internal/ui"
)

func main() {
	// When the app starts itself as a child process to run the built-in
	// hdl_dump / pfsshell, act as that tool and exit (see internal/tools/embedded.go).
	tools.RunEmbeddedTool()
	ui.Run()
}
