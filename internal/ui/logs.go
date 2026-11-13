package ui

import (
	"fmt"
	"github.com/freshness79/ps2hdl-manager/internal/buildinfo"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"
)

// openLogs opens the persistent activity log (rotated at 5 MB) and routes fatal
// panics to crash.log, so problems can be diagnosed after the fact.
func openLogs() (logF, crashF *os.File) {
	dir := config.Dir()
	p := filepath.Join(dir, "ps2hdl.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(p, p+".1")
	}
	logF, _ = os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if logF != nil {
		fmt.Fprintf(logF, "---- %s %s started %s (%s/%s)\n", appTitle, buildinfo.Version, time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH)
	}
	crashF, _ = os.OpenFile(filepath.Join(dir, "crash.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if crashF != nil {
		_ = debug.SetCrashOutput(crashF, debug.CrashOptions{})
	}
	return logF, crashF
}

// truncate shortens s to n runes with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
