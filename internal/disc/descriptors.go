package disc

// Descriptor files that point to the data of a disc image stored elsewhere:
// Alcohol 120% (.mds → .mdf) and CloneCD (.ccd → .img/.sub).

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

// TrackRef locates the first data track of an image.
type TrackRef struct {
	File     string // data file
	Start    int64  // byte offset of the first sector
	End      int64  // byte offset after the last sector
	Declared string // what the descriptor says (for diagnostics)
	Layout   Layout // declared layout (verified against the data by the caller)
	Tracks   int    // tracks on the disc
	Audio    int    // audio tracks (not installed)
}

// siblingWithExt finds base.ext next to path, trying common letter cases.
func siblingWithExt(path, ext string) string {
	base := strings.TrimSuffix(path, filepath.Ext(path))
	for _, e := range []string{ext, strings.ToUpper(ext)} {
		if _, err := os.Stat(base + e); err == nil {
			return base + e
		}
	}
	return ""
}

// ---------------------------------------------------------------- Alcohol MDS

// MDSDataFallback returns the .mdf next to an .mds (or "").
func MDSDataFallback(mds string) string { return siblingWithExt(mds, ".mdf") }

// MDSFor returns the .mds next to an .mdf (or "").
func MDSFor(mdf string) string { return siblingWithExt(mdf, ".mds") }

// ParseMDS reads an Alcohol 120% descriptor (version 1.x) and returns the first
// data track of the first session.
func ParseMDS(path string) (*TrackRef, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 0x58 || string(b[:16]) != "MEDIA DESCRIPTOR" {
		return nil, errors.New("not an Alcohol MDS file (signature \"MEDIA DESCRIPTOR\" missing)")
	}
	ver := fmt.Sprintf("%d.%d", b[16], b[17])
	if b[16] != 1 {
		return nil, fmt.Errorf("MDS version %s not supported (only 1.x; DAEMON Tools/Alcohol 2.x MDS/MDX use a different layout)", ver)
	}
	u16 := func(o int) int { return int(binary.LittleEndian.Uint16(b[o:])) }
	u32 := func(o int) int64 { return int64(binary.LittleEndian.Uint32(b[o:])) }
	medium := u16(0x12)
	sessOff := int(u32(0x50))
	if sessOff+24 > len(b) {
		return nil, errors.New("MDS session block out of range")
	}
	nAll := int(b[sessOff+10])
	trOff := int(u32(sessOff + 20))
	ref := &TrackRef{}
	var first *struct {
		mode, sub            byte
		size                 int
		startOff, extra, ftr int64
	}
	var dataStarts []int64
	for i := 0; i < nAll; i++ {
		o := trOff + i*80
		if o+80 > len(b) {
			return nil, fmt.Errorf("MDS track block %d out of range", i)
		}
		point := b[o+4]
		if point < 1 || point > 99 {
			continue // lead-in entries (A0, A1, A2…)
		}
		ref.Tracks++
		mode := b[o] & 0x0f
		if b[o] == 0xA9 || mode == 9 {
			ref.Audio++
			continue
		}
		start := int64(binary.LittleEndian.Uint64(b[o+40:]))
		dataStarts = append(dataStarts, start)
		if first == nil {
			first = &struct {
				mode, sub            byte
				size                 int
				startOff, extra, ftr int64
			}{b[o], b[o+1], u16(o + 16), start, u32(o + 12), u32(o + 52)}
		}
	}
	if first == nil {
		return nil, errors.New("MDS describes no data track")
	}
	// data file name from the footer ("*.mdf" means: same name as the .mds)
	ref.File = siblingWithExt(path, ".mdf")
	if first.ftr > 0 && int(first.ftr)+16 <= len(b) {
		nameOff, wide := int(u32(int(first.ftr))), u32(int(first.ftr)+4) != 0
		if nameOff > 0 && nameOff < len(b) {
			var name string
			if wide {
				var u []uint16
				for k := nameOff; k+1 < len(b) && binary.LittleEndian.Uint16(b[k:]) != 0; k += 2 {
					u = append(u, binary.LittleEndian.Uint16(b[k:]))
				}
				name = string(utf16.Decode(u))
			} else {
				end := nameOff
				for end < len(b) && b[end] != 0 {
					end++
				}
				name = string(b[nameOff:end])
			}
			if name != "" && !strings.HasPrefix(name, "*") {
				if p := filepath.Join(filepath.Dir(path), filepath.Base(name)); fileExists(p) {
					ref.File = p
				}
			} else if strings.HasPrefix(name, "*.") && ref.File == "" {
				ref.File = siblingWithExt(path, strings.TrimPrefix(name, "*"))
			}
		}
	}
	if ref.File == "" {
		return nil, fmt.Errorf("data file %s.mdf not found next to the .mds", strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	st, err := os.Stat(ref.File)
	if err != nil {
		return nil, err
	}
	ref.Start, ref.End = first.startOff, st.Size()
	for _, s := range dataStarts {
		if s > ref.Start && s < ref.End {
			ref.End = s
		}
	}
	if first.extra > 0 && int(first.extra)+8 <= len(b) { // extra block: pregap, length (sectors)
		if n := u32(int(first.extra) + 4); n > 0 && first.size > 0 {
			if e := ref.Start + n*int64(first.size); e <= st.Size() {
				ref.End = e
			}
		}
	}
	modeName := map[byte]string{0xAA: "MODE1", 0xAB: "MODE2", 0xAC: "MODE2 form 1", 0xAD: "MODE2 form 2", 0xEC: "MODE2 form 1"}[first.mode]
	if modeName == "" {
		modeName = fmt.Sprintf("mode 0x%02X", first.mode)
	}
	kind := "CD"
	if medium >= 0x10 {
		kind = "DVD"
	}
	ref.Declared = fmt.Sprintf("MDS %s, %s, first data track: %s, %d-byte sectors at offset %d", ver, kind, modeName, first.size, ref.Start)
	cdMode := 1
	if first.mode != 0xAA && first.mode&0x0f != 0x0a {
		cdMode = 2
	}
	ref.Layout, _ = LayoutFor(first.size, cdMode)
	return ref, nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// ---------------------------------------------------------------- CloneCD

// CCDFor returns the .ccd belonging to an .img/.sub (or "").
func CCDFor(path string) string { return siblingWithExt(path, ".ccd") }

// ParseCCD reads a CloneCD descriptor: the data is in the .img next to it,
// raw 2352-byte sectors from LBA 0 (subchannel data is in the .sub, not needed).
func ParseCCD(path string) (*TrackRef, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	type track struct{ n, mode, index0, index1 int }
	var tracks []track
	var cur *track
	sawHeader := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		switch {
		case strings.EqualFold(line, "[CloneCD]"):
			sawHeader = true
		case strings.HasPrefix(strings.ToUpper(line), "[TRACK "):
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(line[7:]), "]"))
			tracks = append(tracks, track{n: n, mode: -1, index0: -1, index1: -1})
			cur = &tracks[len(tracks)-1]
		case strings.HasPrefix(line, "["):
			cur = nil
		case cur != nil && strings.Contains(line, "="):
			k, v, _ := strings.Cut(line, "=")
			k, v = strings.ToUpper(strings.TrimSpace(k)), strings.TrimSpace(v)
			n, _ := strconv.Atoi(v)
			switch k {
			case "MODE":
				cur.mode = n
			case "INDEX 0":
				cur.index0 = n
			case "INDEX 1":
				cur.index1 = n
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !sawHeader {
		return nil, errors.New("not a CloneCD descriptor ([CloneCD] section missing)")
	}
	img := siblingWithExt(path, ".img")
	if img == "" {
		return nil, fmt.Errorf("data file %s.img not found next to the .ccd", strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	st, err := os.Stat(img)
	if err != nil {
		return nil, err
	}
	ref := &TrackRef{File: img, Tracks: len(tracks)}
	var first *track
	for i := range tracks {
		t := &tracks[i]
		if t.mode == 0 {
			ref.Audio++
			continue
		}
		if first == nil && t.mode > 0 {
			first = t
		}
	}
	if first == nil {
		return nil, errors.New("the .ccd describes no data track")
	}
	if first.index1 < 0 {
		first.index1 = 0
	}
	ref.Start, ref.End = int64(first.index1)*2352, st.Size()
	for _, t := range tracks { // the track ends where the next one (or its pregap, INDEX 0) begins
		next := t.index1
		if t.index0 >= 0 {
			next = t.index0
		}
		if next > first.index1 && int64(next)*2352 < ref.End {
			ref.End = int64(next) * 2352
		}
	}
	ref.Layout, _ = LayoutFor(2352, first.mode)
	ref.Declared = fmt.Sprintf("CloneCD, track %d MODE%d at sector %d (%d tracks, %d audio)", first.n, first.mode, first.index1, len(tracks), ref.Audio)
	return ref, nil
}

// Section returns a reader over the track data of ref.
func (r *TrackRef) Section() (*os.File, *io.SectionReader, error) {
	f, err := os.Open(r.File)
	if err != nil {
		return nil, nil, err
	}
	if r.End <= r.Start {
		f.Close()
		return nil, nil, fmt.Errorf("empty data track (%d..%d) in %s", r.Start, r.End, filepath.Base(r.File))
	}
	return f, io.NewSectionReader(f, r.Start, r.End-r.Start), nil
}
