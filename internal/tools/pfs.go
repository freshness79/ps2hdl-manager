package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/freshness79/ps2hdl-manager/internal/apa"
)

// PFS wraps pfsshell (github.com/ps2homebrew/pfsshell), used for everything
// hdl_dump cannot do: deleting partitions, formatting, and writing files
// (cover art) into the OPL PFS partition.
type PFS struct {
	Path string // executable to start: this app (see EmbeddedPFS)
	Tool string // built-in tool name (see embedded.go)
}

// pfsDevice converts hdl_dump device names (hdd1:) to what pfsshell expects on Windows.
func pfsDevice(dev string) string {
	if m := regexp.MustCompile(`^hdd(\d+):$`).FindStringSubmatch(dev); m != nil {
		return `\\.\PhysicalDrive` + m[1]
	}
	return dev
}

var pfsNoise = regexp.MustCompile(`^(pfs: Format|hdd: |pfs |ps2fs: |pfs Max)`)

// Run executes a pfsshell script on dev. dir is the local working directory (for put/get).
// Returns the output and an error if pfsshell reported any "(!)" failure, unless the
// failing line matches one of the tolerated substrings.
func (p *PFS) Run(ctx context.Context, dev, dir string, cmds []string, cb LineFunc, tolerate ...string) (string, error) {
	if p.Path == "" {
		return "", fmt.Errorf("built-in pfsshell not available")
	}
	script := "device " + pfsDevice(dev) + "\n" + strings.Join(cmds, "\n") + "\nexit\n"
	traceTool("pfsshell", cmds)
	cmd := toolCommand(ctx, p.Path, p.Tool)
	cmd.Stdin = strings.NewReader(script)
	if dir != "" {
		cmd.Dir = dir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	out := buf.String()
	var errs []string
	for _, raw := range strings.Split(out, "\n") {
		// strip prompts ("> ", "# ", "+OPL:/# ")
		l := regexp.MustCompile(`^(?:\S*# |> )+`).ReplaceAllString(strings.TrimRight(raw, "\r"), "")
		if strings.TrimSpace(l) == "" || pfsNoise.MatchString(l) {
			continue
		}
		if strings.Contains(l, "(!)") && !strings.Contains(l, "Exit code is") {
			ok := false
			for _, t := range tolerate {
				if strings.Contains(l, t) {
					ok = true
				}
			}
			if !ok {
				errs = append(errs, strings.TrimSpace(l))
				if cb != nil {
					cb(strings.TrimSpace(l))
				}
			}
		} else if cb != nil && strings.Contains(l, "created") {
			cb(strings.TrimSpace(l)) // e.g. "Main partition of 128M created."
		}
	}
	if runErr != nil && len(errs) == 0 {
		errs = append(errs, runErr.Error())
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("pfsshell: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

var lsPartRe = regexp.MustCompile(`^\s*[\d.]+\s+[KMGT]iB\s+(.+?)[/*]?\s*$`)

// Partitions lists APA partition names.
func (p *PFS) Partitions(ctx context.Context, dev string) ([]string, error) {
	out, err := p.Run(ctx, dev, "", []string{"ls"}, nil)
	if err != nil {
		return nil, err
	}
	var parts []string
	for _, l := range strings.Split(out, "\n") {
		l = regexp.MustCompile(`^(?:\S*# |> )+`).ReplaceAllString(l, "")
		if m := lsPartRe.FindStringSubmatch(l); m != nil {
			parts = append(parts, m[1])
		}
	}
	return parts, nil
}

// RmPart removes an APA partition (and its sub-partitions).
func (p *PFS) RmPart(ctx context.Context, dev, name string, cb LineFunc) error {
	_, err := p.Run(ctx, dev, "", []string{"rmpart " + Quote(name)}, cb)
	return err
}

// quote protects names with spaces for pfsshell's parser.
func Quote(s string) string {
	if strings.ContainsRune(s, '"') {
		return "'" + s + "'"
	}
	return `"` + s + `"`
}

// Initialize formats the disk with APA + the standard system partitions (DESTRUCTIVE).
func (p *PFS) Initialize(ctx context.Context, dev string, cb LineFunc) error {
	_, err := p.Run(ctx, dev, "", []string{"initialize yes"}, cb)
	return err
}

// artDir is the ART folder inside the OPL partition ("+X" partitions hold it at the
// root, any other partition, e.g. __common, under OPL/).
func ArtDir(part string) string { return oplSubdir(part, "ART") }

// cfgDir is where OPL reads per-game CFG files (compatibility modes, game info).
func CfgDir(part string) string { return oplSubdir(part, "CFG") }

func oplSubdir(part, d string) string {
	if strings.HasPrefix(part, "+") {
		return d
	}
	return "OPL/" + d
}

// Benign pfsshell errors (exact strerror texts): a file to delete/read that is not
// there, a folder that already exists.
var (
	TolMissing = []string{": No such file or directory"}
	tolExists  = []string{": File exists"}
)

var errNoOPL = errors.New("OPL partition not found")

// EnsureOPL creates the OPL partition (128 MB PFS) and its ART/CFG folders if missing.
func (p *PFS) EnsureOPL(ctx context.Context, dev, part string, cb LineFunc) error {
	_, err := p.OPLSession(ctx, dev, part, "", nil, true, cb)
	return err
}

// PartitionExists checks the APA table directly (falls back to pfsshell "ls").
func (p *PFS) PartitionExists(ctx context.Context, dev, part string) (bool, error) {
	if d, err := apa.Scan(dev); err == nil {
		return d.HasPartition(part), nil
	}
	parts, err := p.Partitions(ctx, dev)
	if err != nil {
		return false, err
	}
	for _, x := range parts {
		if x == part {
			return true, nil
		}
	}
	return false, nil
}

// OPLSession runs everything in ONE pfsshell process: [create the OPL partition when
// create is set and it is missing], mount it, make sure ART and CFG exist, run cmds
// (use absolute paths: "cd /ART"; local files are relative to localDir), unmount.
// Without create, a missing partition returns errNoOPL and nothing is run.
func (p *PFS) OPLSession(ctx context.Context, dev, part, localDir string, cmds []string, create bool, cb LineFunc) (string, error) {
	exists, err := p.PartitionExists(ctx, dev, part)
	if err != nil {
		return "", err
	}
	var all []string
	if !exists {
		if !create {
			return "", errNoOPL
		}
		if cb != nil {
			cb("Creating partition " + part + " (128 MB, PFS)…")
		}
		all = append(all, "mkpart "+Quote(part)+" 128M PFS")
	}
	all = append(all, "mount "+Quote(part))
	if !strings.HasPrefix(part, "+") {
		all = append(all, "mkdir /OPL")
	}
	all = append(all, "mkdir /"+ArtDir(part), "mkdir /"+CfgDir(part))
	all = append(all, cmds...)
	all = append(all, "umount")
	tol := append(append([]string{}, TolMissing...), tolExists...)
	return p.Run(ctx, dev, localDir, all, cb, tol...)
}

// rmPutCmds replaces files in a folder: rm (ignored if missing) then put.
func RmPutCmds(dir string, files []string) []string {
	cmds := []string{"cd /" + dir}
	for _, f := range files {
		cmds = append(cmds, "rm "+Quote(f), "put "+Quote(f))
	}
	return cmds
}

var artNameRe = regexp.MustCompile(`([A-Za-z]{4}_\d{3}\.\d{2}_[A-Z0-9]+\.(?:jpg|png))`)

// ListArt returns the file names in the ART folder (empty if there is no OPL partition).
func (p *PFS) ListArt(ctx context.Context, dev, part string) ([]string, error) {
	out, err := p.OPLSession(ctx, dev, part, "", []string{"cd /" + ArtDir(part), "ls"}, false, nil)
	if errors.Is(err, errNoOPL) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, m := range artNameRe.FindAllStringSubmatch(out, -1) {
		files = append(files, m[1])
	}
	return files, nil
}

// PutArt copies files from localDir into the ART folder (overwriting).
func (p *PFS) PutArt(ctx context.Context, dev, part, localDir string, files []string, cb LineFunc) error {
	_, err := p.OPLSession(ctx, dev, part, localDir, RmPutCmds(ArtDir(part), files), true, cb)
	return err
}

// RmArt deletes files from the ART folder.
func (p *PFS) RmArt(ctx context.Context, dev, part string, files []string, cb LineFunc) error {
	cmds := []string{"cd /" + ArtDir(part)}
	for _, f := range files {
		cmds = append(cmds, "rm "+Quote(f))
	}
	_, err := p.OPLSession(ctx, dev, part, "", cmds, false, cb)
	if errors.Is(err, errNoOPL) {
		return nil
	}
	return err
}

// GetCFG reads CFG/<startup>.cfg from the OPL partition ("" if missing).
func (p *PFS) GetCFG(ctx context.Context, dev, part, startup string) (string, error) {
	m, err := p.GetCFGs(ctx, dev, part, []string{startup + ".cfg"})
	return m[startup+".cfg"], err
}

// GetCFGs reads several CFG files in one session (missing ones are absent from the map).
func (p *PFS) GetCFGs(ctx context.Context, dev, part string, names []string) (map[string]string, error) {
	res := map[string]string{}
	if len(names) == 0 {
		return res, nil
	}
	tmp, err := os.MkdirTemp("", "ps2hdl-cfg")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(tmp)
	cmds := []string{"cd /" + CfgDir(part)}
	for _, n := range names {
		cmds = append(cmds, "get "+Quote(n))
	}
	_, err = p.OPLSession(ctx, dev, part, tmp, cmds, false, nil)
	if errors.Is(err, errNoOPL) {
		return res, nil
	}
	for _, n := range names {
		if b, e := os.ReadFile(filepath.Join(tmp, n)); e == nil {
			res[n] = string(b)
		}
	}
	return res, err
}

// PutCFGs writes CFG files (name → content) into the OPL CFG folder.
func (p *PFS) PutCFGs(ctx context.Context, dev, part string, files map[string]string, cb LineFunc) error {
	if len(files) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "ps2hdl-cfg")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	var names []string
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(body), 0o644); err != nil {
			return err
		}
		names = append(names, name)
	}
	_, err = p.OPLSession(ctx, dev, part, tmp, RmPutCmds(CfgDir(part), names), true, cb)
	return err
}
