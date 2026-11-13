// Package apa reads a PlayStation 2 HDD (APA partition table + HD Loader game
// headers) directly, read-only. Layout and rules mirror hdl_dump 0.9.2
// (apa.c, hdl.c, ps2_hdd.h) so results match `hdl_dump toc/hdl_toc`.
package apa

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
)

const (
	sectorSize    = 512
	headerSize    = 1024 // partition header = 2 sectors
	slice2Offs    = 0x10000000
	hdlHeaderOffs = 0x101000
	chunkSectors  = 128 * 1024 * 1024 / sectorSize // 128 MB

	TypeMBR  = 0x0001
	TypePFS  = 0x0100
	TypeHDL  = 0x1337
	FlagSub  = 0x0001
	hdlMagic = 0xdeadfeed
)

var (
	ErrNotAPA = errors.New("not a PlayStation 2 (APA) disk")
	ErrBadAPA = errors.New("corrupted APA partition table")
)

// Partition is one APA partition header (main or sub-partition).
type Partition struct {
	ID     string
	Start  uint32 // sector (relative to the slice)
	Length uint32 // sectors
	Type   uint16
	Flags  uint16
	NSub   uint32
	Main   uint32
	Slice  int
	Subs   []uint32 // lengths of sub-partitions (from the main header)
	// DirtyID: non-zero bytes after the id's terminating NUL (left by old hdl_dump
	// renames); the PS2 APA driver / pfsshell then cannot open or remove the partition
	DirtyID bool
}

// IsMain reports whether this is a main partition (not a sub-partition).
func (p Partition) IsMain() bool { return p.Flags&FlagSub == 0 }

// Game is an HD Loader game (main 0x1337 partition + its header).
type Game struct {
	Partition string // APA partition id, e.g. PP.SLUS-90002..GAME
	Name      string
	Startup   string
	IsDVD     bool
	SizeKB    uint64 // raw image size, like hdl_toc
	Compat    uint8  // compatibility flags bitmask
	DMA       uint16
	Hidden    bool // partition id starts with "__."
	Slice     int
	Start     uint32
	DirtyID   bool // see Partition.DirtyID
}

// Flags renders the compatibility flags like hdl_dump: "+1+2" ("" when none).
func (g Game) Flags() string {
	s := ""
	for i := 0; i < 8; i++ {
		if g.Compat&(1<<uint(i)) != 0 {
			s += "+" + strconv.Itoa(i+1)
		}
	}
	return s
}

// DMAString renders the DMA mode like hdl_dump: "*u4", "*m2" or "".
func (g Game) DMAString() string {
	switch g.DMA % 256 {
	case 32:
		if n := (int(g.DMA) - 32) / 256; n < 3 {
			return "*m" + strconv.Itoa(n)
		}
	case 64:
		if n := (int(g.DMA) - 64) / 256; n < 5 {
			return "*u" + strconv.Itoa(n)
		}
	}
	return ""
}

// Disk is the result of a scan.
type Disk struct {
	SizeMB     uint64
	Partitions []Partition
	Games      []Game
	TotalMB    uint64 // like hdl_toc "total"
	UsedMB     uint64
	FreeMB     uint64
}

// HasPartition reports whether a partition with this id exists.
func (d *Disk) HasPartition(id string) bool {
	for _, p := range d.Partitions {
		if p.ID == id {
			return true
		}
	}
	return false
}

var hddNRe = regexp.MustCompile(`^hdd(\d+):$`)

// OpenPath maps hdl_dump device names (hdd1:) to OS paths.
func OpenPath(dev string) string {
	if m := hddNRe.FindStringSubmatch(dev); m != nil {
		return `\\.\PhysicalDrive` + m[1]
	}
	return dev
}

// Scan reads the partition table and all HDL game headers of dev.
func Scan(dev string) (*Disk, error) {
	f, err := os.Open(OpenPath(dev))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ScanReader(f, deviceSize(f))
}

// ScanReader scans any ReaderAt (sizeBytes 0 = unknown: taken from the MBR).
func ScanReader(r io.ReaderAt, sizeBytes uint64) (*Disk, error) {
	mbr := make([]byte, headerSize)
	if _, err := r.ReadAt(mbr, 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(mbr[4:8], []byte("APA\x00")) {
		return nil, ErrNotAPA
	}
	if sizeBytes == 0 {
		sizeBytes = uint64(le32(mbr[292:])) * sectorSize // mbr.nsector
	}
	sizeKB := sizeBytes / 1024
	d := &Disk{SizeMB: sizeBytes >> 20}
	// two-slice ("toxic") layout for disks > 128 GB, as in apa_toc_read_internal
	twoSlices := false
	if bytes.Equal(mbr[256+32+4+4+8+4+4+72+32+24+60:][:8], []byte("APAEXT\x00\x00")) {
		flags := le32(mbr[256+32+4+4+8+4+4+72+32+24+60+8:])
		twoSlices = flags&1 != 0 && sizeKB > 128*1024*1024
	}
	slices := 1
	if twoSlices {
		slices = 2
	}
	for s := 0; s < slices; s++ {
		var total uint64
		switch {
		case !twoSlices:
			total = sizeKB * 2
		case s == 0:
			total = min(sizeKB, 128*1024*1024-1) * 2
		default:
			total = (sizeKB - 128*1024*1024) * 2
		}
		all, err := readSlice(r, s, total)
		if err != nil {
			return nil, err
		}
		// "__empty" (type 0) partitions are free space left by deletions: hdl_dump
		// (AUTO_DELETE_EMPTY) drops them before computing statistics and listing.
		parts := all[:0:0]
		for _, p := range all {
			if p.Type != 0 {
				parts = append(parts, p)
			}
		}
		d.Partitions = append(d.Partitions, parts...)
		// statistics exactly like apa_setup_statistics + hdl_toc
		totalChunks := total / 2048 / 128
		used := uint64(0)
		for _, p := range parts {
			used += uint64(p.Length / chunkSectors)
		}
		d.TotalMB += totalChunks * 128
		d.UsedMB += used * 128
		if used <= totalChunks {
			d.FreeMB += (totalChunks - used) * 128
		}
		for _, p := range parts {
			if p.Type != TypeHDL || !p.IsMain() {
				continue
			}
			g, err := readGame(r, p)
			if err != nil {
				continue // not an HDL header: hdl_dump skips it too
			}
			d.Games = append(d.Games, g)
		}
	}
	return d, nil
}

func readSlice(r io.ReaderAt, slice int, totalSectors uint64) ([]Partition, error) {
	var parts []Partition
	buf := make([]byte, headerSize)
	sector := uint32(0)
	seen := map[uint32]bool{}
	for {
		off := (int64(sector) + int64(slice)*slice2Offs) * sectorSize
		if _, err := r.ReadAt(buf, off); err != nil {
			return nil, fmt.Errorf("read partition header at sector %d: %w", sector, err)
		}
		if !bytes.Equal(buf[4:8], []byte("APA\x00")) {
			if len(parts) == 0 {
				return nil, ErrNotAPA
			}
			return nil, ErrBadAPA
		}
		if le32(buf[0:]) != checksum(buf) {
			return nil, ErrBadAPA
		}
		p := Partition{
			ID:      cstr(buf[16:48]),
			DirtyID: dirtyID(buf[16:48]),
			Start:   le32(buf[64:]),
			Length:  le32(buf[68:]),
			Type:    le16(buf[72:]),
			Flags:   le16(buf[74:]),
			NSub:    le32(buf[76:]),
			Main:    le32(buf[88:]),
			Slice:   slice,
		}
		if uint64(p.Start) >= totalSectors || uint64(p.Start)+uint64(p.Length) >= totalSectors {
			return nil, ErrBadAPA
		}
		subsOff := 512 // after id/name (256 bytes) and the mbr block (256 bytes): subs[64]{start,length}
		for i := uint32(0); i < p.NSub && i < 64; i++ {
			p.Subs = append(p.Subs, le32(buf[subsOff+int(i)*8+4:]))
		}
		parts = append(parts, p)
		seen[sector] = true
		sector = le32(buf[8:]) // next
		if sector == 0 {
			break
		}
		if seen[sector] || len(parts) > 10000 {
			return nil, ErrBadAPA
		}
	}
	return parts, nil
}

func readGame(r io.ReaderAt, p Partition) (Game, error) {
	buf := make([]byte, 1024)
	off := (int64(p.Start) + hdlHeaderOffs/sectorSize + int64(p.Slice)*slice2Offs) * sectorSize
	if _, err := r.ReadAt(buf, off); err != nil {
		return Game{}, err
	}
	if le32(buf) != hdlMagic {
		return Game{}, errors.New("not an HDL partition")
	}
	g := Game{
		Partition: p.ID,
		Name:      cstr(buf[0x08:0xa8]),
		Startup:   cstr(buf[0xac : 0xac+13]),
		Compat:    buf[0xa9],
		DMA:       le16(buf[0xaa:]),
		IsDVD:     buf[0xec] == 0x14,
		Hidden:    len(p.ID) >= 3 && p.ID[:3] == "__.",
		Slice:     p.Slice,
		Start:     p.Start,
		DirtyID:   p.DirtyID,
	}
	n := int(buf[0xf0])
	var size uint64
	for i := 0; i < n && 0xf5+(i*3+3)*4 <= len(buf); i++ {
		size += uint64(le32(buf[0xf5+(i*3+2)*4:]))
	}
	g.SizeKB = size / 4
	return g, nil
}

func checksum(h []byte) uint32 {
	var sum uint32
	for i := 4; i < headerSize; i += 4 {
		sum += le32(h[i:])
	}
	return sum
}

func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func le16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// dirtyID reports non-zero bytes after the first NUL of a fixed-size id field.
func dirtyID(b []byte) bool {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return false
	}
	for _, c := range b[i:] {
		if c != 0 {
			return true
		}
	}
	return false
}
