package disc

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CueTrack is a track of a CUE sheet.
type CueTrack struct {
	Number int
	Index0 int64  // byte offset of INDEX 00 (pregap stored in the file), -1 if none
	Type   string // "MODE1/2352", "MODE2/2352", "MODE1/2048", "AUDIO"…
	File   string // absolute path of the data file
	Start  int64  // byte offset of INDEX 01 in File
	End    int64  // byte offset where the track ends in File
	Layout Layout // for data tracks
	Data   bool
}

// Cue is a parsed CUE sheet.
type Cue struct {
	Path   string
	Tracks []CueTrack
}

// DataTrack returns the first data track.
func (c *Cue) DataTrack() (*CueTrack, error) {
	for i := range c.Tracks {
		if c.Tracks[i].Data {
			return &c.Tracks[i], nil
		}
	}
	return nil, fmt.Errorf("CUE sheet %s has no data track", filepath.Base(c.Path))
}

// AudioTracks counts the audio tracks (not stored by HDL: only the data track is).
func (c *Cue) AudioTracks() int {
	n := 0
	for _, t := range c.Tracks {
		if !t.Data {
			n++
		}
	}
	return n
}

var (
	cueFileRe  = regexp.MustCompile(`(?i)^\s*FILE\s+(?:"([^"]+)"|(\S+))\s*(\w*)\s*$`)
	cueTrackRe = regexp.MustCompile(`(?i)^\s*TRACK\s+(\d+)\s+(\S+)\s*$`)
	cueIndexRe = regexp.MustCompile(`(?i)^\s*INDEX\s+(\d+)\s+(\d+):(\d+):(\d+)\s*$`)
)

// sectorSizeOf returns bytes per sector and CD mode of a CUE track type.
func sectorSizeOf(typ string) (size, mode int, data bool, err error) {
	switch strings.ToUpper(typ) {
	case "MODE1/2048":
		return 2048, 1, true, nil
	case "MODE1/2352":
		return 2352, 1, true, nil
	case "MODE2/2336":
		return 2336, 2, true, nil
	case "MODE2/2352", "CDI/2352":
		return 2352, 2, true, nil
	case "MODE2/2048", "MODE2/2324", "MODE2/2328":
		return 0, 2, true, fmt.Errorf("track type %s is not supported", typ)
	case "AUDIO", "CDG":
		return 2352, 0, false, nil
	}
	return 0, 0, false, fmt.Errorf("unknown track type %q", typ)
}

// ParseCue reads a CUE sheet. Relative file names are resolved next to the
// sheet; a missing file is reported with the name the sheet expects.
func ParseCue(path string) (*Cue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := &Cue{Path: path}
	var file string
	var cur *CueTrack
	type pending struct {
		idx  int
		size int64
	}
	var inFile []pending // tracks of the current FILE, to compute their End
	closeFile := func() error {
		if file == "" || len(inFile) == 0 {
			return nil
		}
		st, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("data file %q named in the CUE sheet not found", filepath.Base(file))
		}
		for i, p := range inFile {
			t := &c.Tracks[p.idx]
			if i+1 < len(inFile) { // ends where the next track (or its stored pregap) begins
				nt := c.Tracks[inFile[i+1].idx]
				t.End = nt.Start
				if nt.Index0 >= 0 && nt.Index0 < t.End {
					t.End = nt.Index0
				}
			} else {
				t.End = st.Size()
			}
			if t.End < t.Start {
				return fmt.Errorf("track %d: INDEX 01 beyond the end of %s", t.Number, filepath.Base(file))
			}
		}
		inFile = nil
		return nil
	}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimPrefix(sc.Text(), "\ufeff")
		switch {
		case cueFileRe.MatchString(text):
			if err := closeFile(); err != nil {
				return nil, err
			}
			m := cueFileRe.FindStringSubmatch(text)
			name := m[1]
			if name == "" {
				name = m[2]
			}
			if !filepath.IsAbs(name) {
				name = filepath.Join(filepath.Dir(path), name)
			}
			file, cur = name, nil
		case cueTrackRe.MatchString(text):
			m := cueTrackRe.FindStringSubmatch(text)
			if file == "" {
				return nil, fmt.Errorf("line %d: TRACK before any FILE", line)
			}
			n, _ := strconv.Atoi(m[1])
			size, mode, data, err := sectorSizeOf(m[2])
			if err != nil {
				return nil, fmt.Errorf("line %d: track %d: %v", line, n, err)
			}
			t := CueTrack{Number: n, Type: strings.ToUpper(m[2]), File: file, Data: data, Start: -1, Index0: -1}
			if data {
				t.Layout, _ = LayoutFor(size, mode)
			} else {
				t.Layout = Layout{SectorSize: int64(size), Name: "audio"}
			}
			c.Tracks = append(c.Tracks, t)
			cur = &c.Tracks[len(c.Tracks)-1]
		case cueIndexRe.MatchString(text):
			m := cueIndexRe.FindStringSubmatch(text)
			if cur == nil {
				return nil, fmt.Errorf("line %d: INDEX outside a TRACK", line)
			}
			mm, _ := strconv.Atoi(m[2])
			ss, _ := strconv.Atoi(m[3])
			ff, _ := strconv.Atoi(m[4])
			off := int64((mm*60+ss)*75+ff) * cur.Layout.SectorSize
			idx, _ := strconv.Atoi(m[1])
			if idx == 0 {
				cur.Index0 = off
				continue
			}
			if idx != 1 {
				continue
			}
			cur.Start = off
			inFile = append(inFile, pending{idx: len(c.Tracks) - 1})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := closeFile(); err != nil {
		return nil, err
	}
	if len(c.Tracks) == 0 {
		return nil, fmt.Errorf("no tracks in CUE sheet %s", filepath.Base(path))
	}
	for _, t := range c.Tracks {
		if t.Start < 0 {
			return nil, fmt.Errorf("track %d has no INDEX 01", t.Number)
		}
	}
	return c, nil
}

// CueReferencing returns a .cue in the same folder whose FILE entries name data
// (used to group a .bin/.img with its sheet), or "".
func CueReferencing(data string) string {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(data), "*.[cC][uU][eE]"))
	for _, m := range matches {
		c, err := ParseCue(m)
		if err != nil {
			continue
		}
		for _, t := range c.Tracks {
			if same, _ := sameFile(t.File, data); same {
				return m
			}
		}
	}
	return ""
}

func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}
