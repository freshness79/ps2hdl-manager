package builtin

// Format plug-in for PlayStation Store packages: recognised and explained, never
// installed (they are encrypted store packages, not disc images).

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/freshness79/ps2hdl-manager/internal/formats"
)

type pkgFormat struct{}

func init() { formats.Register(pkgFormat{}) }

func (pkgFormat) Name() string         { return "PKG" }
func (pkgFormat) Extensions() []string { return []string{".pkg"} }

func pkgKind(r io.ReaderAt) string {
	b := make([]byte, 4)
	if _, err := r.ReadAt(b, 0); err != nil {
		return ""
	}
	switch string(b) {
	case "\x7fPKG":
		return "PS3 PKG"
	case "\x7fCNT":
		return "PS4 PKG"
	}
	return ""
}

func (pkgFormat) Sniff(r io.ReaderAt, _ int64) bool { return pkgKind(r) != "" }

func (pkgFormat) Probe(_ context.Context, path string) (*formats.Info, error) {
	kind := "PKG"
	if f, err := os.Open(path); err == nil {
		if k := pkgKind(f); k != "" {
			kind = k
		}
		f.Close()
	}
	return &formats.Info{
		Unsupported: kind + ": not supported (encrypted store package) — use a disc ISO",
		Details:     kind + " files are encrypted PlayStation Store packages, not disc images: they cannot be installed with HD Loader/OPL.",
	}, nil
}

func (pkgFormat) Prepare(context.Context, string, *formats.Info, formats.Env) (*formats.Prepared, error) {
	return nil, errors.New("PKG files cannot be installed")
}
