// Package formats is the plug-in registry for disc image formats.
//
// Every input format the installer accepts (ISO, CUE/BIN, CHD, NRG, PKG, …) is a
// Format registered with Register, normally from an init() function in its own
// file. The installer only talks to this interface, so adding a format means
// adding one file — nothing else changes:
//
//	func init() { formats.Register(myFormat{}) }
//
//	type myFormat struct{}
//
//	func (myFormat) Name() string         { return "XYZ" }
//	func (myFormat) Extensions() []string { return []string{".xyz"} }
//	func (myFormat) Probe(ctx context.Context, path string) (*formats.Info, error) {
//		// cheap analysis when the file is queued: startup ID, CD/DVD, problems…
//		return &formats.Info{Startup: "SLUS_123.45", Media: formats.DVD}, nil
//	}
//	func (myFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
//		// at install time: return a file hdl_dump can read (ISO / CUE / NRG / …),
//		// converting into env.WorkDir if needed, and a Cleanup for temporary files
//		return &formats.Prepared{Path: path}, nil
//	}
//
// Optional interfaces: Sniffer (recognise files by content, regardless of the
// extension) and Native (hdl_dump reads the format itself; the installer may then
// ask hdl_dump for the startup ID when Probe could not find it).
package formats

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Media is the disc type an image is installed as.
type Media int

const (
	Unknown Media = iota
	CD
	DVD
)

func (m Media) String() string {
	switch m {
	case CD:
		return "CD"
	case DVD:
		return "DVD"
	}
	return "?"
}

// Format is a disc image format plug-in.
type Format interface {
	// Name is shown to the user ("ISO", "CHD", …).
	Name() string
	// Extensions handled, lower case with the dot (".iso").
	Extensions() []string
	// Probe analyses a file when it is added to the queue. It must be reasonably
	// fast (no conversion). A returned error is shown as the item's status.
	Probe(ctx context.Context, path string) (*Info, error)
	// Prepare turns the file into something hdl_dump inject can read. It is only
	// called for items without Unsupported/Problem. Long work reports env.Progress.
	Prepare(ctx context.Context, path string, info *Info, env Env) (*Prepared, error)
}

// Sniffer is implemented by formats that can recognise a file by its content
// (magic numbers, footers…), whatever its extension.
type Sniffer interface {
	Sniff(r io.ReaderAt, size int64) bool
}

// Grouper is implemented by formats made of several files (CUE+BIN, MDS+MDF,
// CCD+IMG+SUB): Primary returns the file that represents the whole image, so
// dropping a folder queues each game once. Return path itself when it stands alone.
type Grouper interface {
	Primary(path string) string
}

// Native is implemented by formats hdl_dump reads directly.
type Native interface {
	NativeToHDLDump() bool
}

// Info is the result of Probe.
type Info struct {
	Startup string // detected startup ID ("SLUS_900.01"), "" if unknown
	Media   Media  // Unknown: let the installer decide (size heuristic)

	// IDAtPrepare: the ID can only be read by Prepare (e.g. compressed images);
	// an empty ID is not a problem at queue time.
	IDAtPrepare bool
	// NeedsID: hdl_dump cannot find the ID by itself: installing requires one.
	NeedsID bool

	Notes       []string // informational notes for the status line
	Problem     string   // blocking problem (install refused until fixed)
	Unsupported string   // the file can never be installed (reason)
	Fix         *Fix     // optional repair for Problem
	Details     string   // full diagnostics text (Info button)

	Private any // plug-in state carried from Probe to Prepare
}

// Fix is a repair action offered for a Problem.
type Fix struct {
	Label   string // button label ("Fix")
	Confirm string // explanation shown before running it
	// Run performs the repair and returns the path of the repaired file, which
	// replaces the original in the queue (and is probed again).
	Run func(ctx context.Context, progress func(float64)) (newPath string, err error)
}

// Env is what Prepare may use.
type Env struct {
	WorkDir  string        // folder for large temporary files
	Startup  string        // ID typed/confirmed by the user ("" if none)
	Progress func(float64) // 0..1, may be nil
	Log      func(string)  // may be nil
	Status   func(string)  // short status for the queue row, may be nil
}

// Report forwards progress (0..1).
func (e Env) Report(f float64) {
	if e.Progress != nil {
		e.Progress(f)
	}
}

// Logf logs a line.
func (e Env) Logf(format string, a ...any) {
	if e.Log != nil {
		e.Log(fmt.Sprintf(format, a...))
	}
}

// SetStatus updates the queue row status.
func (e Env) SetStatus(s string) {
	if e.Status != nil {
		e.Status(s)
	}
}

// Prepared is the result of Prepare.
type Prepared struct {
	Path    string // file to pass to hdl_dump inject
	Startup string // ID found while preparing ("" = keep the queued one)
	Media   Media  // Unknown = keep the queued choice
	NeedsID bool   // after preparing, the ID turned out to be required
	Cleanup func() // removes temporary files; may be nil
}

// Close runs Cleanup once.
func (p *Prepared) Close() {
	if p != nil && p.Cleanup != nil {
		p.Cleanup()
		p.Cleanup = nil
	}
}

// ---------------------------------------------------------------- registry

var (
	mu    sync.RWMutex
	list  []Format
	byExt = map[string]Format{}
)

// Register adds a format. It panics on a duplicate extension: two plug-ins
// claiming the same extension is a programming error.
func Register(f Format) {
	mu.Lock()
	defer mu.Unlock()
	for _, e := range f.Extensions() {
		e = strings.ToLower(e)
		if !strings.HasPrefix(e, ".") {
			panic("formats: extension without dot: " + e)
		}
		if old, ok := byExt[e]; ok {
			panic(fmt.Sprintf("formats: %s claimed by both %s and %s", e, old.Name(), f.Name()))
		}
		byExt[e] = f
	}
	list = append(list, f)
}

// All returns the registered formats sorted by name.
func All() []Format {
	mu.RLock()
	defer mu.RUnlock()
	out := append([]Format(nil), list...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Extensions returns every registered extension, sorted.
func Extensions() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(byExt))
	for e := range byExt {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// Supported reports whether a path has a registered extension.
func Supported(path string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := byExt[strings.ToLower(filepath.Ext(path))]
	return ok
}

// For returns the format of path: content sniffers win over the extension (a
// .iso that is really a Nero image is handled as NRG), then the extension decides.
func For(path string) (Format, error) {
	if f := sniff(path); f != nil {
		return f, nil
	}
	mu.RLock()
	f, ok := byExt[strings.ToLower(filepath.Ext(path))]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported file type %q", filepath.Ext(path))
	}
	return f, nil
}

func sniff(path string) Format {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || st.IsDir() {
		return nil
	}
	for _, f := range All() {
		if s, ok := f.(Sniffer); ok && s.Sniff(fh, st.Size()) {
			return f
		}
	}
	return nil
}

// Primary maps a file to the one representing its image (see Grouper).
func Primary(path string) string {
	mu.RLock()
	f, ok := byExt[strings.ToLower(filepath.Ext(path))]
	mu.RUnlock()
	if g, isG := f.(Grouper); ok && isG {
		if p := g.Primary(path); p != "" {
			return p
		}
	}
	return path
}

// IsNative reports whether hdl_dump reads f directly.
func IsNative(f Format) bool {
	n, ok := f.(Native)
	return ok && n.NativeToHDLDump()
}

// Summary lists the formats for help texts ("CHD (.chd), ISO (.iso), …").
func Summary() string {
	var parts []string
	for _, f := range All() {
		parts = append(parts, fmt.Sprintf("%s (%s)", f.Name(), strings.Join(f.Extensions(), " ")))
	}
	return strings.Join(parts, ", ")
}
