package builtin

// Shared machinery for plug-ins whose data is a track inside a file (or any
// io.ReaderAt): analysis at queue time and conversion to a plain ISO with
// 2048-byte sectors at install time. Every plug-in output written to the PS2
// disk is such an ISO (checked again by the installer with disc.Verify2048).

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
)

const cdMaxBytes = 700 * 1024 * 1024 // larger 2048-byte images are DVDs

// trackState is carried from Probe to Prepare.
type trackState struct {
	open   func() (io.ReaderAt, int64, func(), error) // data track: reader, length, close
	layout disc.Layout
	diag   *disc.Diag
	// passthrough: the source file already is a plain 2048-byte ISO (no copy)
	passthrough string
}

// probeTrack analyses a data track. what describes the container for the
// details ("Nero image, track 1 at byte 307200"); notes are added to the queue row.
func probeTrack(name, what string, st *trackState, notes ...string) (*formats.Info, error) {
	r, length, closeFn, err := st.open()
	if err != nil {
		return nil, err
	}
	defer closeFn()
	l, err := disc.DetectLayout(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	st.layout = l
	d, err := disc.InspectReader(name, r, true)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	st.diag = d
	sectors := l.Sectors(length)
	info := &formats.Info{
		Startup: d.Startup, NeedsID: d.Err112, Private: st, Media: formats.CD,
		Notes: append([]string{}, notes...),
	}
	if !l.Raw() && sectors*2048 > cdMaxBytes {
		info.Media = formats.DVD
	}
	if l.Raw() {
		info.Notes = append(info.Notes, fmt.Sprintf("%d-byte sectors → 2048", l.SectorSize))
	} else if st.passthrough == "" {
		info.Notes = append(info.Notes, "→ 2048-byte ISO at install")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\nData track: %d sectors, %s (%.1f MB of game data once converted)\n\n%s",
		what, sectors, l.Name, float64(sectors*2048)/(1<<20), d.String())
	info.Details = sb.String()
	switch {
	case d.UDFOnly:
		info.Problem = "UDF-only image: no ISO9660 file system (needed by hdl_dump and the PS2)"
	case d.Err111 && st.passthrough != "":
		info.Problem = "Error 111: broken ISO header — click Fix"
	}
	return info, nil
}

// prepareTrack writes the data track as a temporary 2048-byte ISO in env.WorkDir
// (or passes a plain ISO through) and repairs a broken path table on the copy.
func prepareTrack(ctx context.Context, path string, info *formats.Info, env formats.Env, status string) (*formats.Prepared, error) {
	st, _ := info.Private.(*trackState)
	if st == nil {
		return nil, fmt.Errorf("%s: image not analysed", filepath.Base(path))
	}
	if st.passthrough != "" {
		return &formats.Prepared{Path: st.passthrough}, nil
	}
	r, length, closeFn, err := st.open()
	if err != nil {
		return nil, err
	}
	defer closeFn()
	if err := os.MkdirAll(env.WorkDir, 0o755); err != nil {
		return nil, err
	}
	iso := filepath.Join(env.WorkDir, "ps2hdl-"+strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".iso")
	env.SetStatus(status)
	env.Logf("%s: %s, %d sectors → %s", filepath.Base(path), st.layout.Name, st.layout.Sectors(length), filepath.Base(iso))
	if err := disc.WriteISO(ctx, r, length, st.layout, iso, env.Report); err != nil {
		return nil, fmt.Errorf("converting to a 2048-byte ISO: %w", err)
	}
	p := &formats.Prepared{Path: iso, Cleanup: func() { _ = os.Remove(iso) }}
	if d, _ := disc.Inspect(iso); d != nil {
		if d.Err111 && !d.UDFOnly {
			env.Logf("%s: broken ISO path table repaired on the temporary copy", filepath.Base(path))
			_ = disc.RepairPathTableInPlace(iso)
			d, _ = disc.Inspect(iso)
		}
		if d != nil {
			p.Startup, p.NeedsID = d.Startup, d.Err112
		}
	}
	if info.Media == formats.DVD {
		p.Media = formats.DVD
	} else {
		p.Media = formats.CD
	}
	return p, nil
}

// fileTrack opens [start,end) of a file as the data track (end < 0 = file end).
func fileTrack(path string, start, end int64) func() (io.ReaderAt, int64, func(), error) {
	return func() (io.ReaderAt, int64, func(), error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, nil, err
		}
		e := end
		if e < 0 {
			st, err := f.Stat()
			if err != nil {
				f.Close()
				return nil, 0, nil, err
			}
			e = st.Size()
		}
		if e <= start {
			f.Close()
			return nil, 0, nil, fmt.Errorf("%s: empty data track (bytes %d..%d)", filepath.Base(path), start, e)
		}
		return io.NewSectionReader(f, start, e-start), e - start, func() { f.Close() }, nil
	}
}

// isPlainISO reports whether a whole file is a 2048-byte ISO usable as is.
func isPlainISO(path string, l disc.Layout) bool {
	if l.Raw() {
		return false
	}
	return disc.Verify2048(path) == nil
}
