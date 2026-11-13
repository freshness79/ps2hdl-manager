package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// BatchOp is one game modification for the embedded batch modifier
// (internal/hdldump/batch.c): rename (two-step via a temporary name) or flags.
type BatchOp struct {
	Game    Game
	NewName string // rename when != ""
	Mask    int    // flags when >= 0
	CleanID bool   // zero the garbage after the partition id (see Game.DirtyID)
	// HeaderDir rewrites the HDD-OSD header from the icon.sys/list.ico files in
	// this dir (parts without a file are kept); takes precedence over the others.
	HeaderDir string
	Err       error // result, filled by Batch
}

// CanBatch reports whether ops on these games can run in one batch process:
// their partition positions must be known from the Go scanner.
func (h *HDL) CanBatch(games ...Game) bool {
	for _, g := range games {
		if !g.Located {
			return false
		}
	}
	return true
}

// Batch applies all ops in a single hdl_dump process: the disk is opened and the
// partition table read once. progress (may be nil) is called after each op.
// The returned error is only for failures of the whole batch; per-op results are in ops[i].Err.
func (h *HDL) Batch(ctx context.Context, dev string, ops []BatchOp, progress func(done int)) error {
	var in strings.Builder
	for _, op := range ops {
		g := op.Game
		if op.HeaderDir != "" {
			fmt.Fprintf(&in, "H\t%d\t%d\t%s\n", g.Slice, g.Start, absPath(op.HeaderDir))
		} else if op.CleanID {
			fmt.Fprintf(&in, "Z\t%d\t%d\n", g.Slice, g.Start)
		} else if op.NewName != "" {
			name := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(op.NewName)
			fmt.Fprintf(&in, "R\t%d\t%d\t%s\t%s\n", g.Slice, g.Start, tempName(), name)
		} else {
			fmt.Fprintf(&in, "F\t%d\t%d\t%d\n", g.Slice, g.Start, op.Mask)
		}
	}
	traceTool("hdl_batch", []string{dev, fmt.Sprintf("(%d ops)", len(ops))})
	cmd := toolCommand(ctx, h.Path, ToolBatch, dev)
	cmd.Dir = hdlWorkDir()
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	seen := make([]bool, len(ops))
	warn := make([]string, len(ops))
	var fatal error
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", 4)
		if f[0] == "FATAL" {
			msg := sc.Text()
			if len(f) == 3 || len(f) == 4 {
				msg = f[len(f)-1] + " (code " + f[1] + ")"
			}
			fatal = errors.New(msg)
			continue
		}
		if len(f) < 2 {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 1 || n > len(ops) {
			continue
		}
		switch f[0] {
		case "OK":
			seen[n-1] = true
		case "ERR":
			seen[n-1] = true
			msg := "operation failed"
			if len(f) == 4 {
				msg = f[3]
			}
			code, _ := strconv.Atoi(f[2])
			if warn[n-1] != "" {
				msg += "; " + warn[n-1]
			}
			ops[n-1].Err = fmt.Errorf("%s (hdl_dump code %d)", msg, code)
		case "WARN":
			if len(f) == 4 {
				warn[n-1] = f[3]
			}
			continue
		}
		if progress != nil {
			progress(n)
		}
	}
	werr := cmd.Wait()
	if fatal != nil {
		return fatal
	}
	for i := range ops {
		if !seen[i] && ops[i].Err == nil {
			ops[i].Err = errors.New("not executed (batch interrupted)")
		}
	}
	var ee *exec.ExitError
	if werr != nil && !(errors.As(werr, &ee) && ee.ExitCode() == 1) {
		return werr
	}
	return nil
}
