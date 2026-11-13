package builtin

// Format plug-in for Nero images (.nrg, versions 1 and 2, DAO and TAO).
//
// The chunk table at the end of the file is parsed to find the first track; its
// sector layout (2048 / 2352 raw) is detected from the ISO9660 volume descriptor.
// At install time the data track is copied to a temporary 2048-byte ISO, so every
// NRG variant works (hdl_dump's own NRG reader only handles one specific layout).

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
	"io"
	"os"
	"path/filepath"
)

type nrgFormat struct{}

func init() { formats.Register(nrgFormat{}) }

func (nrgFormat) Name() string         { return "NRG (Nero)" }
func (nrgFormat) Extensions() []string { return []string{".nrg"} }

func (nrgFormat) Sniff(r io.ReaderAt, size int64) bool {
	_, _, err := nrgChunkTable(r, size)
	return err == nil
}

// nrgTrack is the first track of a Nero image: byte range of its sectors.
type nrgTrack struct {
	Start, End int64
}

// nrgChunkTable returns the offset of the chunk table and the image version.
func nrgChunkTable(r io.ReaderAt, size int64) (int64, int, error) {
	if size >= 12 {
		b := make([]byte, 12)
		if _, err := r.ReadAt(b, size-12); err == nil && string(b[:4]) == "NER5" {
			off := int64(binary.BigEndian.Uint64(b[4:]))
			if off > 0 && off < size-12 {
				return off, 2, nil
			}
		}
	}
	if size >= 8 {
		b := make([]byte, 8)
		if _, err := r.ReadAt(b, size-8); err == nil && string(b[:4]) == "NERO" {
			off := int64(binary.BigEndian.Uint32(b[4:]))
			if off > 0 && off < size-8 {
				return off, 1, nil
			}
		}
	}
	return 0, 0, errors.New("not a Nero image (no NER5/NERO footer)")
}

// nrgFirstTrack walks the chunk list and returns the first track (DAO or TAO).
func nrgFirstTrack(r io.ReaderAt, size int64) (*nrgTrack, error) {
	off, ver, err := nrgChunkTable(r, size)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, 8)
	for off+8 <= size {
		if _, err := r.ReadAt(hdr, off); err != nil {
			return nil, err
		}
		id, n := string(hdr[:4]), int64(binary.BigEndian.Uint32(hdr[4:]))
		if id == "END!" {
			break
		}
		if n < 0 || off+8+n > size {
			return nil, fmt.Errorf("corrupted Nero chunk %q", id)
		}
		body := make([]byte, n)
		if _, err := r.ReadAt(body, off+8); err != nil {
			return nil, err
		}
		switch id {
		case "DAOX", "DAOI": // disc-at-once: header 22 bytes, then one entry per track
			w := 8 // offset width
			if id == "DAOI" || ver == 1 {
				w = 4
			}
			if t := 22 + 12 + 6; len(body) >= t+3*w {
				e := body[t:]
				get := func(i int) int64 {
					if w == 8 {
						return int64(binary.BigEndian.Uint64(e[i*8:]))
					}
					return int64(binary.BigEndian.Uint32(e[i*4:]))
				}
				return validTrack(get(1), get(2), size) // index1 .. end of track
			}
		case "ETN2", "ETNF": // track-at-once: offset, length, …
			if id == "ETN2" && len(body) >= 16 {
				s := int64(binary.BigEndian.Uint64(body[0:]))
				return validTrack(s, s+int64(binary.BigEndian.Uint64(body[8:])), size)
			}
			if id == "ETNF" && len(body) >= 8 {
				s := int64(binary.BigEndian.Uint32(body[0:]))
				return validTrack(s, s+int64(binary.BigEndian.Uint32(body[4:])), size)
			}
		}
		off += 8 + n
	}
	return nil, errors.New("Nero image without track information (DAO/TAO chunk missing)")
}

func validTrack(start, end, size int64) (*nrgTrack, error) {
	if start < 0 || end <= start || end > size {
		return nil, fmt.Errorf("invalid track range %d..%d in Nero image", start, end)
	}
	return &nrgTrack{Start: start, End: end}, nil
}

func (nrgFormat) Probe(_ context.Context, path string) (*formats.Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	tr, err := nrgFirstTrack(f, st.Size())
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("Nero image: %w", err)
	}
	what := fmt.Sprintf("Nero image %s: first track at byte %d, %d bytes", filepath.Base(path), tr.Start, tr.End-tr.Start)
	return probeTrack(path, what, &trackState{open: fileTrack(path, tr.Start, tr.End)})
}

func (nrgFormat) Prepare(ctx context.Context, path string, info *formats.Info, env formats.Env) (*formats.Prepared, error) {
	return prepareTrack(ctx, path, info, env, "Extracting Nero data track…")
}
