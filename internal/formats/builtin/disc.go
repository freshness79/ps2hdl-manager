package builtin

// Plain disc images: ISO, UDF, BIN/CUE, BIN and IMG.
//
// A plain ISO/UDF with 2048-byte sectors is written as is; anything stored with
// raw sectors (2352/2336/2448) or behind a CUE sheet is converted to a 2048-byte
// ISO first.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
)

type imageKind int

const (
	kindISO imageKind = iota // .iso / .udf: whole file
	kindBIN                  // .bin: whole file, grouped with its .cue
	kindIMG                  // .img: grouped with .ccd (CloneCD) or .cue
)

type imageFormat struct {
	name string
	exts []string
	kind imageKind
}

func init() {
	formats.Register(imageFormat{"ISO", []string{".iso"}, kindISO})
	formats.Register(imageFormat{"UDF", []string{".udf"}, kindISO})
	formats.Register(imageFormat{"BIN", []string{".bin"}, kindBIN})
	formats.Register(imageFormat{"IMG", []string{".img"}, kindIMG})
	formats.Register(cueFormat{})
}

func (f imageFormat) Name() string         { return f.name }
func (f imageFormat) Extensions() []string { return f.exts }

// Primary: a .bin/.img described by a CUE sheet, or an .img of a CloneCD set,
// is installed through its descriptor.
func (f imageFormat) Primary(path string) string {
	if f.kind == kindIMG {
		if ccd := disc.CCDFor(path); ccd != "" {
			return ccd
		}
	}
	if f.kind != kindISO {
		if cue := disc.CueReferencing(path); cue != "" {
			return cue
		}
	}
	return path
}

func (f imageFormat) Probe(_ context.Context, path string) (*formats.Info, error) {
	if f.kind == kindIMG {
		if ccd := disc.CCDFor(path); ccd != "" { // dropped directly: use the CloneCD set
			return ccdFormat{}.Probe(context.Background(), ccd)
		}
	}
	st := &trackState{open: fileTrack(path, 0, -1)}
	layout, err := detectFile(path)
	if err != nil {
		return nil, err
	}
	if isPlainISO(path, layout) {
		st.passthrough = path
	}
	what := fmt.Sprintf("%s image %s", f.name, filepath.Base(path))
	var notes []string
	if f.kind == kindBIN && layout.Raw() {
		notes = append(notes, "no CUE: layout from the data")
	}
	info, err := probeTrack(path, what, st, notes...)
	if err != nil {
		return nil, err
	}
	if d := st.diag; d.Err111 && st.passthrough != "" && !d.UDFOnly {
		info.Fix = repairISOFix(path)
	}
	return info, nil
}

func (f imageFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
	if _, isCCD := info.Private.(*ccdState); isCCD {
		return ccdFormat{}.Prepare(ctx, path, info, env)
	}
	return prepareTrack(ctx, path, info, env, "Converting to 2048-byte sectors…")
}

func detectFile(path string) (disc.Layout, error) {
	open := fileTrack(path, 0, -1)
	r, _, closeFn, err := open()
	if err != nil {
		return disc.Layout{}, err
	}
	defer closeFn()
	return disc.DetectLayout(r)
}

// ---------------------------------------------------------------- CUE sheet

type cueFormat struct{}

func (cueFormat) Name() string         { return "BIN/CUE" }
func (cueFormat) Extensions() []string { return []string{".cue"} }

func (cueFormat) Probe(_ context.Context, path string) (*formats.Info, error) {
	c, err := disc.ParseCue(path)
	if err != nil {
		return nil, fmt.Errorf("CUE sheet: %w", err)
	}
	t, err := c.DataTrack()
	if err != nil {
		return nil, err
	}
	st := &trackState{open: fileTrack(t.File, t.Start, t.End)}
	what := fmt.Sprintf("CUE sheet %s: %d track(s); track %d %s in %s at byte %d",
		filepath.Base(path), len(c.Tracks), t.Number, t.Type, filepath.Base(t.File), t.Start)
	var notes []string
	if n := c.AudioTracks(); n > 0 {
		notes = append(notes, fmt.Sprintf("%d audio track(s) skipped", n))
	}
	info, err := probeTrack(path, what, st, notes...)
	if err != nil {
		return nil, err
	}
	if n := c.AudioTracks(); n > 0 {
		info.Details = fmt.Sprintf("%d audio track(s) are not installed: HD Loader/OPL store only the data track.\n", n) + info.Details
	}
	if st.layout != t.Layout && t.Layout.SectorSize != 0 {
		info.Details = fmt.Sprintf("Note: the CUE sheet declares %s but the data is %s: the data wins.\n\n", t.Layout.Name, st.layout.Name) + info.Details
	}
	return info, nil
}

func (cueFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
	return prepareTrack(ctx, path, info, env, "Converting BIN/CUE to 2048-byte sectors…")
}

// repairISOFix writes <name>.fixed.iso with a valid path table (original untouched).
func repairISOFix(path string) *formats.Fix {
	dst := strings.TrimSuffix(path, filepath.Ext(path)) + ".fixed.iso"
	return &formats.Fix{
		Label: "Fix",
		Confirm: fmt.Sprintf("hdl_dump cannot read this ISO (error 111: broken path table in the volume descriptor).\n\n"+
			"A repaired copy will be written next to the original:\n%s\n\nThe original file is not modified. "+
			"The copy needs as much free space as the ISO.", dst),
		Run: func(_ context.Context, progress func(float64)) (string, error) {
			return dst, disc.RepairPathTable(path, dst, progress)
		},
	}
}
