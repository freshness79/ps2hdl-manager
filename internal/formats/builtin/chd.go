package builtin

// Format plug-in for MAME CHD images: decompressed to a 2048-byte ISO at install time.

import (
	"bytes"
	"context"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"io"
	"os"
)

// CHD is set by the application (built-in CHD codec, v3 to v5).
var CHD = &tools.CHDTool{}

type chdFormat struct{}

func init() { formats.Register(chdFormat{}) }

func (chdFormat) Name() string         { return "CHD" }
func (chdFormat) Extensions() []string { return []string{".chd"} }

func (chdFormat) Sniff(r io.ReaderAt, _ int64) bool {
	b := make([]byte, 8)
	_, err := r.ReadAt(b, 0)
	return err == nil && bytes.Equal(b, []byte("MComprHD"))
}

func (chdFormat) Probe(ctx context.Context, path string) (*formats.Info, error) {
	kind, err := CHD.Kind(ctx, path)
	if err != nil {
		return nil, err
	}
	info := &formats.Info{IDAtPrepare: true, Media: formats.CD, Private: kind,
		Notes:   []string{"CHD (" + kind + ")", "decompressed to a 2048-byte ISO during install"},
		Details: "CHD image, " + kind + ".\nIt is decompressed to a temporary 2048-byte ISO in the work folder at install time; the startup ID is read from it."}
	if kind == "DVD" {
		info.Media = formats.DVD
	}
	return info, nil
}

func (chdFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
	env.SetStatus("Decompressing CHD…")
	iso, err := CHD.ToISO(ctx, path, env.WorkDir, func(line string) {
		if p, ok := tools.ParsePercent(line); ok {
			env.Report(p)
			return
		}
		env.Logf("%s", line)
	})
	if err != nil {
		return nil, err
	}
	p := &formats.Prepared{Path: iso, Cleanup: func() { _ = os.Remove(iso) }}
	d, _ := disc.Inspect(iso)
	if d != nil && d.Err111 {
		env.Logf("Repairing ISO header of the extracted image")
		_ = disc.RepairPathTableInPlace(iso)
		d, _ = disc.Inspect(iso)
	}
	if d != nil {
		p.Startup, p.NeedsID = d.Startup, d.Err112
	}
	if kind, _ := info.Private.(string); kind == "DVD" {
		p.Media = formats.DVD
	} else if st, err := os.Stat(iso); err == nil && st.Size() > 700*1024*1024 {
		p.Media = formats.DVD
	}
	return p, nil
}
