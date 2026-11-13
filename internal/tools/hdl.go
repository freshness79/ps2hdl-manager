package tools

import (
	"bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"github.com/freshness79/ps2hdl-manager/internal/apa"
	"github.com/freshness79/ps2hdl-manager/internal/system"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// HDL wraps calls to the built-in hdl_dump.
type HDL struct {
	Path    string // executable to start: this app (see EmbeddedHDL)
	Tool    string // built-in tool name (see embedded.go)
	Version string
	caps    map[string]bool // commands advertised by this build (nil = unknown, assume all)
}

// Disk is a drive that can be selected as target.

// Game is an installed HDL game.
type Game struct {
	Type      string // CD / DVD
	Startup   string // e.g. SLUS_900.01
	SizeKB    int64
	Flags     string
	DMA       string
	Name      string
	Partition string // APA partition id (known exactly when read by the Go scanner)
	Hidden    bool
	// position of the game partition, known when read by the Go scanner:
	// lets the embedded batch modifier address it without a name lookup
	Located bool
	Slice   int
	Start   uint32
	DirtyID bool // partition id has garbage after the NUL: pfsshell can't remove it until cleaned
}

// LineFunc receives every output line (stdout+stderr) of hdl_dump.
type LineFunc func(line string)

var (
	percentRe = regexp.MustCompile(`(\d{1,3})(?:\.\d+)?\s*%`)
	ansiRe    = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	cmdRe     = regexp.MustCompile(`(?m)^  ([a-z][a-z0-9_]+)\s+\S`)
	versionRe = regexp.MustCompile(`hdl_dump[-_ ]v?(\d+\.\d+(?:\.\d+)?)`)
)

// exitDescriptions maps hdl_dump exit codes (100 + RET_xxx, see retcodes.h).
var exitDescriptions = map[int]string{
	100: "invalid command or arguments (command probably not supported by this hdl_dump build)",
	101: "not a PlayStation 2 (APA) device",
	102: "not an HD Loader partition",
	103: "partition / game not found",
	104: "bad device name format",
	105: "unrecognized device (macOS: use /dev/diskN, not /dev/rdiskN, and run with sudo)",
	106: "not enough free space",
	107: "corrupted APA partition table",
	109: "operation interrupted",
	110: "a partition with this name already exists",
	111: "bad ISO file system (broken path table?) — use Install → Fix to create a repaired copy",
	112: "not recognized as a PlayStation CD/DVD — pass the startup ID (filled in automatically by the installer)",
	113: "SYSTEM.CNF not in the expected format",
	114: "input unsupported or not found (check the device path, e.g. /dev/disk4)",
	115: "operation not allowed",
	120: "file not found",
	121: "missing linked file (e.g. the BIN referenced by a CUE)",
	122: "data beyond the 128 GB mark",
	133: "titles with audio tracks must be converted to a single cue/bin",
	255: "generic error (permissions? run as root/administrator)",
}

// run executes hdl_dump streaming output line by line to cb.
// Progress lines terminated by '\r' are emitted as separate lines.
func (h *HDL) Run(ctx context.Context, cb LineFunc, args ...string) (string, error) {
	return h.runIn(ctx, "", cb, args...)
}

// runIn is Run with hdl_dump started in dir, where it looks for the HDD-OSD
// header files (icon.sys, list.ico, ...) to embed; "" means a clean empty dir.
func (h *HDL) runIn(ctx context.Context, dir string, cb LineFunc, args ...string) (string, error) {
	if h.Path == "" {
		return "", fmt.Errorf("built-in hdl_dump not available")
	}
	traceTool("hdl_dump", args)
	cmd := toolCommand(ctx, h.Path, h.Tool, args...)
	cmd.Dir = dir
	if dir == "" {
		cmd.Dir = hdlWorkDir()
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		sc.Split(scanCRLF)
		for sc.Scan() {
			line := ansiRe.ReplaceAllString(sc.Text(), "")
			buf.WriteString(line + "\n")
			if cb != nil {
				cb(line)
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()

	if err := cmd.Start(); err != nil {
		pw.Close()
		wg.Wait()
		return "", err
	}
	err := cmd.Wait()
	pw.Close()
	wg.Wait()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code := ee.ExitCode()
			desc := exitDescriptions[code]
			if desc == "" {
				desc = "error"
			}
			last := lastLine(buf.String())
			if last != "" {
				desc += " — " + last
			}
			name := "(no command)"
			if len(args) > 0 {
				name = args[0]
			}
			return buf.String(), fmt.Errorf("hdl_dump %s: exit %d: %s", name, code, desc)
		}
		return buf.String(), err
	}
	return buf.String(), nil
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(ls) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(ls[i]); t != "" {
			if len(t) > 160 {
				t = t[:160]
			}
			return t
		}
	}
	return ""
}

// scanCRLF splits on '\n' or '\r' (progress bars use '\r').
func scanCRLF(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		j := i + 1
		if data[i] == '\r' && j < len(data) && data[j] == '\n' {
			j++
		}
		return j, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Probe runs hdl_dump without arguments and reads version and available commands.
func (h *HDL) Probe(ctx context.Context) {
	out, _ := h.Run(ctx, nil) // usage exits with code 100: expected
	h.Version = ""
	if m := versionRe.FindStringSubmatch(out); m != nil {
		h.Version = m[1]
	}
	caps := map[string]bool{}
	for _, m := range cmdRe.FindAllStringSubmatch(out, -1) {
		caps[m[1]] = true
	}
	if len(caps) >= 3 {
		h.caps = caps
	} else {
		h.caps = nil
	}
}

// Supports reports whether this hdl_dump build has the given command.
func (h *HDL) Supports(cmd string) bool {
	if h.caps == nil {
		return true
	}
	return h.caps[cmd]
}

// Query lists drives. Uses "hdl_dump query" when available (Windows builds),
// otherwise enumerates the system disks itself (macOS / Linux).
func (h *HDL) Query(ctx context.Context, cb LineFunc) ([]system.Disk, error) {
	if !h.Supports("query") {
		if cb != nil {
			cb("This hdl_dump build has no 'query' command: listing system disks directly.")
		}
		return system.SystemDisks(ctx)
	}
	out, err := h.Run(ctx, cb, "query")
	if err != nil {
		return nil, err
	}
	var disks []system.Disk
	re := regexp.MustCompile(`^\s*(hdd\d+:)\s*(.*)$`)
	for _, l := range strings.Split(out, "\n") {
		if m := re.FindStringSubmatch(l); m != nil {
			disks = append(disks, system.Disk{Device: m[1], Info: strings.TrimSpace(m[2])})
		}
	}
	return disks, nil
}

// Usage is the disk capacity reported by hdl_toc (MB).
type Usage struct{ Total, Used, Free int64 }

var usageRe = regexp.MustCompile(`total\s*(\d+)\s*MB,\s*used\s*(\d+)\s*MB,\s*available\s*(\d+)\s*MB`)

func parseUsage(out string) *Usage {
	m := usageRe.FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	u := &Usage{}
	u.Total, _ = strconv.ParseInt(m[1], 10, 64)
	u.Used, _ = strconv.ParseInt(m[2], 10, 64)
	u.Free, _ = strconv.ParseInt(m[3], 10, 64)
	return u
}

// List returns the installed games and disk usage. Tries the CSV format (0.9.x) first.
func (h *HDL) List(ctx context.Context, dev string, cb LineFunc) ([]Game, *Usage, error) {
	// fast path: read the partition table and game headers directly (no process,
	// exact partition ids). Falls back to hdl_dump for anything it cannot read.
	d, scanErr := apa.Scan(dev)
	if scanErr == nil {
		return scanGames(d), &Usage{Total: int64(d.TotalMB), Used: int64(d.UsedMB), Free: int64(d.FreeMB)}, nil
	}
	if friendly := diskError(dev, scanErr); friendly != nil {
		return nil, nil, friendly // missing / no permission / not PS2: hdl_dump would only add noise
	}
	out, err := h.Run(ctx, cb, "hdl_toc", dev, "--csv")
	if err == nil {
		if g := parseHDLTocCSV(out); len(g) > 0 {
			return g, parseUsage(out), nil
		}
		if g := parseHDLToc(out); len(g) > 0 {
			return g, parseUsage(out), nil
		}
		if strings.Contains(strings.ToLower(out), "type") {
			return nil, parseUsage(out), nil // empty disk
		}
	}
	out, err2 := h.Run(ctx, cb, "hdl_toc", dev)
	if err2 != nil {
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, err2
	}
	return parseHDLToc(out), parseUsage(out), nil
}

func normFlags(f string) string {
	f = strings.TrimSpace(f)
	if f == "" || f == "0" {
		return ""
	}
	if !strings.HasPrefix(f, "+") {
		f = "+" + f
	}
	return f
}

// parseHDLTocCSV parses "DVD;2457600KB;  1+2;*u4;SLUS_900.01 ;Name".
func parseHDLTocCSV(out string) []Game {
	var games []Game
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, ";")
		if len(f) < 6 {
			continue
		}
		t := strings.TrimSpace(f[0])
		if t != "CD" && t != "DVD" {
			continue
		}
		g := Game{Type: t}
		g.SizeKB, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(f[1]), "KB"), 10, 64)
		g.Flags = normFlags(f[2])
		g.DMA = strings.TrimSpace(f[3])
		g.Startup = strings.TrimSpace(f[4])
		g.Name = strings.TrimSpace(strings.Join(f[5:], ";"))
		games = append(games, g)
	}
	return games
}

var (
	tokRe   = regexp.MustCompile(`\S+`)
	flagsRe = regexp.MustCompile(`^\+?\d+(\+\d+)*$`)
	dmaRe   = regexp.MustCompile(`^\*[um]\d$`)
	sizeRe  = regexp.MustCompile(`^(\d+)KB$`)
)

// parseHDLToc parses the plain-text output of both layouts:
//
//	0.9.x : "DVD 2457600KB  1+2 *u4 SLUS_900.01  Name"
//	older : "DVD SLUS_900.01  2457600KB +1+2 *u4 Name"
func parseHDLToc(out string) []Game {
	var games []Game
	for _, l := range strings.Split(out, "\n") {
		idx := tokRe.FindAllStringIndex(l, -1)
		if len(idx) < 4 {
			continue
		}
		tok := func(i int) string { return l[idx[i][0]:idx[i][1]] }
		t := tok(0)
		if t != "CD" && t != "DVD" {
			continue
		}
		g := Game{Type: t}
		i := 1
		newLayout := sizeRe.MatchString(tok(1))
		if newLayout {
			g.SizeKB, _ = strconv.ParseInt(sizeRe.FindStringSubmatch(tok(1))[1], 10, 64)
			i = 2
		} else {
			g.Startup = tok(1)
			if !sizeRe.MatchString(tok(2)) {
				continue
			}
			g.SizeKB, _ = strconv.ParseInt(sizeRe.FindStringSubmatch(tok(2))[1], 10, 64)
			i = 3
		}
		if i < len(idx) && flagsRe.MatchString(tok(i)) {
			g.Flags = normFlags(tok(i))
			i++
		}
		if i < len(idx) && dmaRe.MatchString(tok(i)) {
			g.DMA = tok(i)
			i++
		}
		if newLayout {
			if i >= len(idx) {
				continue
			}
			g.Startup = tok(i)
			i++
		}
		if i < len(idx) {
			g.Name = strings.TrimSpace(l[idx[i][0]:])
		}
		games = append(games, g)
	}
	return games
}

// Inject installs an ISO/CUE on the disk.
func (h *HDL) Inject(ctx context.Context, dev, name, file, startup, flags, dma string, isDVD bool, cb LineFunc) error {
	return h.InjectWithHeader(ctx, dev, name, file, startup, flags, dma, isDVD, "", cb)
}

// InjectWithHeader is Inject embedding the HDD-OSD header files found in
// headerDir (icon.sys and list.ico, see gamedb.HeaderDir); "" embeds none.
func (h *HDL) InjectWithHeader(ctx context.Context, dev, name, file, startup, flags, dma string, isDVD bool, headerDir string, cb LineFunc) error {
	cmd := "inject_cd"
	if isDVD {
		cmd = "inject_dvd"
	}
	args := []string{cmd, dev, name, absPath(file)}
	if startup != "" {
		args = append(args, startup)
	}
	if flags != "" { // compatibility modes, e.g. "+2+3+6"
		args = append(args, flags)
	}
	if dma != "" {
		args = append(args, dma)
	}
	_, err := h.runIn(ctx, headerDir, cb, args...)
	return err
}

// Rename renames an installed game.
//
// hdl_dump stores partition names upper-cased and compares them case-insensitively,
// so a direct rename to a name differing only by case (or colliding with the
// upper-cased partition id) fails with "partition already exists". To avoid it the
// game is first renamed to a random temporary name, then to the final one. If the
// second step fails the original name is restored.
func (h *HDL) Rename(ctx context.Context, dev, oldName, newName string, cb LineFunc) error {
	tmp := tempName()
	if _, err := h.Run(ctx, cb, "modify", dev, oldName, tmp); err != nil {
		return fmt.Errorf("step 1 (→ %s): %w", tmp, err)
	}
	if _, err := h.Run(ctx, cb, "modify", dev, tmp, newName); err != nil {
		if _, rerr := h.Run(ctx, cb, "modify", dev, tmp, oldName); rerr != nil {
			return fmt.Errorf("step 2 failed (%v) and restore failed: game left as %q: %w", err, tmp, rerr)
		}
		return fmt.Errorf("step 2 (→ %s): %w", newName, err)
	}
	return nil
}

func tempName() string {
	b := make([]byte, 4)
	_, _ = crand.Read(b)
	return fmt.Sprintf("TMP_%X", b)
}

// Delete removes an installed game (not available in every build).
func (h *HDL) Delete(ctx context.Context, dev, name string, cb LineFunc) error {
	_, err := h.Run(ctx, cb, "delete", dev, name)
	return err
}

// Info returns details about a game.
func (h *HDL) Info(ctx context.Context, dev, name string, cb LineFunc) (string, error) {
	return h.Run(ctx, cb, "info", dev, name)
}

// Extract dumps a game to an ISO file.
func (h *HDL) Extract(ctx context.Context, dev, name, out string, cb LineFunc) error {
	_, err := h.Run(ctx, cb, "extract", dev, name, absPath(out))
	return err
}

// TOC returns the partition table.
func (h *HDL) TOC(ctx context.Context, dev string, cb LineFunc) (string, error) {
	return h.Run(ctx, cb, "toc", dev)
}

// Initialize formats the disk for PS2 (DESTRUCTIVE, not in every build).
func (h *HDL) Initialize(ctx context.Context, dev string, cb LineFunc) error {
	_, err := h.Run(ctx, cb, "initialize", dev)
	return err
}

// CDVDInfo reads the startup ID from an image via hdl_dump.
func (h *HDL) CDVDInfo(ctx context.Context, file string) (string, error) {
	return h.Run(ctx, nil, "cdvd_info", absPath(file))
}

// ParsePercent extracts a percentage (0-1) from a progress line.
func ParsePercent(line string) (float64, bool) {
	m := percentRe.FindAllStringSubmatch(line, -1)
	if len(m) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[len(m)-1][1], 64)
	if err != nil || v > 100 {
		return 0, false
	}
	return v / 100, true
}

// "0x1337 01020000.: 12  4096MB PP.SLUS-90002..GAME" — the name may contain spaces.
var tocPartRe = regexp.MustCompile(`^0x1337\s+\S+\s+\d+\s+\d+MB (.+?)\s*$`)

// scanGames converts scanner results to Game values.
func scanGames(d *apa.Disk) []Game {
	games := make([]Game, 0, len(d.Games))
	for _, g := range d.Games {
		typ := "CD"
		if g.IsDVD {
			typ = "DVD"
		}
		games = append(games, Game{
			Type: typ, Startup: g.Startup, SizeKB: int64(g.SizeKB),
			Flags: g.Flags(), DMA: g.DMAString(), Name: g.Name,
			Partition: g.Partition, Hidden: g.Hidden,
			Located: true, Slice: g.Slice, Start: g.Start, DirtyID: g.DirtyID,
		})
	}
	return games
}

// HDLPartitions lists the HDL game partitions (type 0x1337).
func (h *HDL) HDLPartitions(ctx context.Context, dev string) ([]string, error) {
	if d, err := apa.Scan(dev); err == nil {
		var parts []string
		for _, p := range d.Partitions {
			if p.Type == apa.TypeHDL && p.IsMain() {
				parts = append(parts, p.ID)
			}
		}
		return parts, nil
	}
	out, err := h.Run(ctx, nil, "toc", dev)
	if err != nil {
		return nil, err
	}
	var parts []string
	for _, l := range strings.Split(out, "\n") {
		if m := tocPartRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			parts = append(parts, m[1])
		}
	}
	return parts, nil
}

// HDLPartName is an exact port of hdl_dump's hdl_pname() (hdl.c):
// prefix "PP." (visible) or "__." (hidden), startup copied by position
// (SLUS_900.02 → SLUS-90002), name truncated to 16 bytes, upper-cased,
// non-alphanumerics → '_'.
func HDLPartName(startup, name, prefix string) string {
	const idmax = 32
	b := make([]byte, 0, idmax)
	b = append(b, prefix[:3]...)
	sid := []byte("SLUS-00000")
	if startup != "" {
		st := append([]byte(startup), make([]byte, 11)...) // pad like a C buffer
		copy(sid[0:8], st[0:8])
		sid[8], sid[9] = st[9], st[10]
	}
	b = append(b, sid...)
	b[2] = '.'
	b[7] = '-'
	b = append(b, '.', '.')
	nm := []byte(name)
	if len(nm) > idmax-1-3-10-2 {
		nm = nm[:idmax-1-3-10-2]
	}
	b = append(b, nm...)
	for i := 3; i < len(b); i++ {
		c := b[i]
		switch {
		case i < 7:
			if c >= 'a' && c <= 'z' {
				c -= 32
			} else if !(c >= 'A' && c <= 'Z') {
				c = 'X'
			}
		case i > 7 && i < 13:
			if !(c >= '0' && c <= '9') {
				c = '0'
			}
		case i > 14:
			if c >= 'a' && c <= 'z' {
				c -= 32
			} else if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				c = '_'
			}
		}
		b[i] = c
	}
	// a NUL in the copied startup ends the C string early
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// FindGamePartition picks the APA partition of game g among parts.
// Returns the match ("" if not certain) and the plausible candidates.
func FindGamePartition(g Game, parts []string) (string, []string) {
	for _, pre := range []string{"PP.", "__."} {
		want := HDLPartName(g.Startup, g.Name, pre)
		for _, p := range parts {
			if p == want {
				return p, []string{p}
			}
		}
	}
	// same ID segment (as hdl_dump would write it)
	idSeg := HDLPartName(g.Startup, "", "PP.")[3:13]
	nameSeg := HDLPartName(g.Startup, g.Name, "PP.")[15:]
	var byID, byName []string
	for _, p := range parts {
		if len(p) >= 13 && strings.EqualFold(p[3:13], idSeg) {
			byID = append(byID, p)
		}
		if len(p) > 15 && nameSeg != "" && strings.EqualFold(p[15:], nameSeg) {
			byName = append(byName, p)
		}
	}
	switch {
	case len(byID) == 1:
		return byID[0], byID
	case len(byID) > 1:
		for _, p := range byID { // several copies with the same ID: use the name
			for _, q := range byName {
				if p == q {
					return p, byID
				}
			}
		}
		return "", byID
	case len(byName) == 1:
		return byName[0], byName
	}
	return "", nil
}

// traceOut receives a line per tool launch when PS2HDL_TRACE is set (diagnostics);
// the app points it at the activity log file — never at the console.
var TraceOut io.Writer = io.Discard

func traceTool(tool string, args []string) {
	if os.Getenv("PS2HDL_TRACE") != "" {
		fmt.Fprintf(TraceOut, "TRACE %s %s\n", tool, strings.Join(args, " | "))
	}
}

// diskError turns common scan failures into an actionable message (nil = unknown,
// let hdl_dump try).
func diskError(dev string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("disk %s not found: is it still connected? Use \"Detect disks\" to refresh the list", dev)
	case errors.Is(err, fs.ErrPermission):
		hint := "run the app as root or add your user to the 'disk' group"
		switch runtime.GOOS {
		case "windows":
			hint = "run the app as Administrator"
		case "darwin":
			hint = "start the app with sudo (raw disk access needs root on macOS)"
		}
		return fmt.Errorf("no permission to read %s: %s", dev, hint)
	case errors.Is(err, apa.ErrNotAPA):
		return fmt.Errorf("%s is not a PlayStation 2 (APA) disk. To use it, format it with Disk → Initialize (this erases it)", dev)
	}
	return nil
}

// absPath: hdl_dump runs in its own working folder (hdlWorkDir), so file paths
// must not be relative.
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
