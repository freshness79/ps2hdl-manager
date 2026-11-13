package tools

import (
	"context"
	"github.com/freshness79/ps2hdl-manager/internal/hdldump"
	"github.com/freshness79/ps2hdl-manager/internal/pfsshell"
	"os"
	"os/exec"
	"path/filepath"
)

// The app has NO command-line options: started by the user it always opens the
// GUI and ignores any argument.
//
// The built-in hdl_dump / pfsshell still run in their own process (their C code
// uses globals and exit()): the app starts itself as a child with the internal
// environment variable below, set only on that child. Only then are the
// arguments handed to the compiled-in tool.
const toolEnv = "PS2HDL_INTERNAL_TOOL"

const (
	ToolHDL   = "hdl_dump"
	ToolBatch = "hdl_batch" // many game modifications in one process (internal/hdldump/batch.c)
	ToolPFS   = "pfsshell"
)

// runEmbeddedTool acts as the requested tool and exits, if this process is one.
func RunEmbeddedTool() {
	tool := os.Getenv(toolEnv)
	if tool == "" {
		return
	}
	_ = os.Unsetenv(toolEnv)
	args := os.Args[1:]
	switch tool {
	case ToolHDL:
		os.Exit(hdldump.Main(append([]string{"hdl_dump"}, args...)))
	case ToolBatch:
		os.Exit(hdldump.BatchMain(append([]string{"hdl_batch"}, args...)))
	case ToolPFS:
		os.Exit(pfsshell.Main(append([]string{"pfsshell"}, args...)))
	}
	os.Exit(2)
}

// toolCommand prepares a child process: this executable (path) acting as the
// built-in tool. Tests may pass a stand-in program with tool == "".
func toolCommand(ctx context.Context, path, tool string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	if tool != "" {
		cmd.Env = append(os.Environ(), toolEnv+"="+tool)
	}
	hideWindow(cmd)
	return cmd
}

// hdlWorkDir is the private, empty working folder hdl_dump runs in. hdl_dump
// writes a debug "./info.sys" on every install and picks up optional
// "./boot.elf", "./list.ico", "./icon.sys"… from its current folder: run from the
// user's folder it would leave files there and could embed unrelated ones in the
// installed game. File paths passed to hdl_dump are always absolute.
func hdlWorkDir() string {
	d := filepath.Join(os.TempDir(), "ps2hdl-manager-hdl")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return ""
	}
	for _, f := range []string{"info.sys", "boot.elf", "boot.kelf", "boot.kirx", "list.ico", "del.ico", "icon.sys", "system.cnf"} {
		_ = os.Remove(filepath.Join(d, f)) // never let a leftover file end up in a game
	}
	return d
}

// selfPath is the path of this executable (used to launch the embedded tools).
func SelfPath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return p
}

// embeddedHDL returns an HDL wrapper using the built-in hdl_dump.
func EmbeddedHDL() *HDL {
	return &HDL{Path: SelfPath(), Tool: ToolHDL}
}

// embeddedPFS returns a PFS wrapper using the built-in pfsshell.
func EmbeddedPFS() *PFS {
	return &PFS{Path: SelfPath(), Tool: ToolPFS}
}
