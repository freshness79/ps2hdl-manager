package builtin

// Images described by a separate descriptor file:
//   - Alcohol 120%: .mds (descriptor) + .mdf (data)
//   - CloneCD:      .ccd (descriptor) + .img (data) + .sub (subchannel, not needed)

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
)

func init() {
	formats.Register(mdsFormat{})
	formats.Register(ccdFormat{})
}

func headerIs(r io.ReaderAt, magic string) bool {
	b := make([]byte, len(magic))
	_, err := r.ReadAt(b, 0)
	return err == nil && bytes.Equal(b, []byte(magic))
}

// ---------------------------------------------------------------- MDS / MDF

type mdsFormat struct{}

func (mdsFormat) Name() string         { return "MDF/MDS (Alcohol)" }
func (mdsFormat) Extensions() []string { return []string{".mds", ".mdf"} }

func (mdsFormat) Sniff(r io.ReaderAt, _ int64) bool { return headerIs(r, "MEDIA DESCRIPTOR") }

func (mdsFormat) Primary(path string) string {
	if isExt(path, ".mdf") {
		if mds := disc.MDSFor(path); mds != "" {
			return mds
		}
	}
	return path
}

func (mdsFormat) Probe(_ context.Context, path string) (*formats.Info, error) {
	if isExt(path, ".mdf") { // data file without (or instead of) its descriptor
		if mds := disc.MDSFor(path); mds != "" {
			path = mds
		} else {
			st := &trackState{open: fileTrack(path, 0, -1)}
			return probeTrack(path, fmt.Sprintf("MDF %s without .mds: layout detected from the data", filepath.Base(path)), st,
				"no .mds: layout from the data")
		}
	}
	ref, err := disc.ParseMDS(path)
	if err != nil {
		mdf := disc.MDSDataFallback(path)
		if mdf == "" {
			return nil, fmt.Errorf("MDS descriptor: %w", err)
		}
		// unreadable descriptor (e.g. MDS 2.x): try the .mdf content directly
		st := &trackState{open: fileTrack(mdf, 0, -1)}
		info, perr := probeTrack(mdf, fmt.Sprintf("MDS descriptor not usable (%v); layout detected from %s", err, filepath.Base(mdf)), st,
			"MDS not readable: layout detected from the .mdf")
		if perr != nil {
			return nil, fmt.Errorf("MDS descriptor: %v; reading the .mdf directly: %w", err, perr)
		}
		return info, nil
	}
	return probeRef(path, ref)
}

func (mdsFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
	return prepareTrack(ctx, path, info, env, "Converting MDF to 2048-byte sectors…")
}

// ---------------------------------------------------------------- CloneCD

type ccdFormat struct{}

type ccdState struct{ *trackState }

func (ccdFormat) Name() string         { return "CCD/IMG/SUB (CloneCD)" }
func (ccdFormat) Extensions() []string { return []string{".ccd", ".sub"} }

func (ccdFormat) Sniff(r io.ReaderAt, _ int64) bool { return headerIs(r, "[CloneCD]") }

func (ccdFormat) Primary(path string) string {
	if ccd := disc.CCDFor(path); ccd != "" {
		return ccd
	}
	return path
}

func (ccdFormat) Probe(_ context.Context, path string) (*formats.Info, error) {
	if !isExt(path, ".ccd") {
		ccd := disc.CCDFor(path)
		if ccd == "" {
			return nil, fmt.Errorf("%s: subchannel data only; the .ccd and .img with the same name are needed", filepath.Base(path))
		}
		path = ccd
	}
	ref, err := disc.ParseCCD(path)
	if err != nil {
		return nil, fmt.Errorf("CloneCD descriptor: %w", err)
	}
	info, err := probeRef(path, ref)
	if err != nil {
		return nil, err
	}
	info.Private = &ccdState{info.Private.(*trackState)}
	return info, nil
}

func (ccdFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
	if s, ok := info.Private.(*ccdState); ok {
		c := *info
		c.Private = s.trackState
		return prepareTrack(ctx, path, &c, env, "Converting CloneCD image to 2048-byte sectors…")
	}
	return prepareTrack(ctx, path, info, env, "Converting CloneCD image to 2048-byte sectors…")
}

// probeRef analyses the data track a descriptor points to.
func probeRef(path string, ref *disc.TrackRef) (*formats.Info, error) {
	st := &trackState{open: fileTrack(ref.File, ref.Start, ref.End)}
	var notes []string
	if ref.Audio > 0 {
		notes = append(notes, fmt.Sprintf("%d audio track(s) skipped", ref.Audio))
	}
	info, err := probeTrack(path, ref.Declared+" ("+filepath.Base(ref.File)+")", st, notes...)
	if err != nil {
		return nil, err
	}
	if ref.Audio > 0 {
		info.Details = fmt.Sprintf("%d audio track(s) are not installed: HD Loader/OPL store only the data track.\n", ref.Audio) + info.Details
	}
	if ref.Layout.SectorSize != 0 && ref.Layout != st.layout {
		info.Details = fmt.Sprintf("Note: the descriptor declares %s but the data is %s: the data wins.\n\n", ref.Layout.Name, st.layout.Name) + info.Details
	}
	return info, nil
}

func isExt(path, ext string) bool { return len(path) >= len(ext) && equalFold(filepath.Ext(path), ext) }

func equalFold(a, b string) bool { return bytes.EqualFold([]byte(a), []byte(b)) }
