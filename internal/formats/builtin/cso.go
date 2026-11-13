package builtin

// Compressed ISO images: CSO (deflate, v1/v2) and ZSO (LZ4). Decompressed to a
// 2048-byte ISO at install time.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
)

type csoFormat struct{}

func init() { formats.Register(csoFormat{}) }

func (csoFormat) Name() string         { return "CSO/ZSO" }
func (csoFormat) Extensions() []string { return []string{".cso", ".zso", ".ciso", ".ziso"} }

func (csoFormat) Sniff(r io.ReaderAt, _ int64) bool {
	return headerIs(r, "CISO") || headerIs(r, "ZISO")
}

func csoTrack(path string) func() (io.ReaderAt, int64, func(), error) {
	return func() (io.ReaderAt, int64, func(), error) {
		c, err := disc.OpenCISO(path)
		if err != nil {
			return nil, 0, nil, err
		}
		return c, c.Size, func() { c.Close() }, nil
	}
}

func (csoFormat) Probe(_ context.Context, path string) (*formats.Info, error) {
	c, err := disc.OpenCISO(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	what := fmt.Sprintf("%s image %s: %.1f MB uncompressed, %d-byte blocks", c.Name(), filepath.Base(path), float64(c.Size)/(1<<20), c.BlockSize)
	c.Close()
	return probeTrack(path, what, &trackState{open: csoTrack(path)}, "compressed")
}

func (csoFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
	return prepareTrack(ctx, path, info, env, "Decompressing…")
}
