package chd

// Reading of the older CHD v3 and v4 formats (MAME before 0.146): a header of
// 120 (v3) or 108 (v4) bytes, followed by a plain map of 16-byte entries and
// the "EndOfListCookie". Hunks are zlib (raw deflate) compressed as a whole,
// stored, a repeated 8-byte pattern ("mini") or a copy of an earlier hunk.
// CD images keep the same frame layout as v5 (2352 data + 96 subcode bytes per
// frame), so everything above ReadHunk works unchanged.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

const (
	v3HeaderSize = 120
	v4HeaderSize = 108

	v34CompNone     = 0
	v34CompZlib     = 1
	v34CompZlibPlus = 2
	v34CompAV       = 3

	v34TypeCompressed   = 1
	v34TypeUncompressed = 2
	v34TypeMini         = 3
	v34TypeSelf         = 4
	v34TypeParent       = 5
	v34TypeMask         = 0x0f
	v34FlagNoCRC        = 0x10

	v34FlagHasParent = 0x00000001

	cdFrameBytes = 2448 // CD frame as stored: 2352 sector + 96 subcode
)

type v34Entry struct {
	offset uint64
	crc    uint32
	length uint32
	flags  uint8
}

func (c *File) readHeaderV34() error {
	size := v4HeaderSize
	if c.Version == 3 {
		size = v3HeaderSize
	}
	h := make([]byte, size)
	if _, err := c.f.ReadAt(h, 0); err != nil {
		return ErrNotCHD
	}
	if int(binary.BigEndian.Uint32(h[8:])) != size {
		return ErrNotCHD
	}
	flags := binary.BigEndian.Uint32(h[16:])
	c.v34Codec = binary.BigEndian.Uint32(h[20:])
	c.HunkCount = binary.BigEndian.Uint32(h[24:])
	c.LogicalBytes = binary.BigEndian.Uint64(h[28:])
	c.metaOffset = binary.BigEndian.Uint64(h[36:])
	if c.Version == 3 {
		c.HunkBytes = binary.BigEndian.Uint32(h[76:])
		copy(c.SHA1[:], h[80:100])
		copy(c.md5[:], h[44:60])
	} else {
		c.HunkBytes = binary.BigEndian.Uint32(h[44:])
		copy(c.SHA1[:], h[48:68])
		copy(c.RawSHA1[:], h[88:108])
	}
	if flags&v34FlagHasParent != 0 {
		return ErrParent
	}
	switch c.v34Codec {
	case v34CompNone, v34CompZlib, v34CompZlibPlus:
	case v34CompAV:
		return fmt.Errorf("%w (A/V laserdisc CHD)", ErrUnsupported)
	default:
		return fmt.Errorf("%w (compression %d)", ErrUnsupported, c.v34Codec)
	}
	if c.HunkBytes == 0 || c.HunkBytes >= 65536*256 || c.HunkCount == 0 ||
		uint64(c.HunkBytes)*uint64(c.HunkCount) < c.LogicalBytes {
		return ErrNotCHD
	}
	st, err := c.f.Stat()
	if err != nil {
		return err
	}
	mapBytes := int64(c.HunkCount) * 16
	if int64(size)+mapBytes+16 > st.Size() {
		return fmt.Errorf("chd: map larger than the file (truncated?)")
	}
	raw := make([]byte, mapBytes+16)
	if _, err := c.f.ReadAt(raw, int64(size)); err != nil {
		return err
	}
	if !bytes.Equal(raw[mapBytes:mapBytes+15], []byte("EndOfListCookie")) {
		return fmt.Errorf("chd: map end marker missing (corrupted file)")
	}
	c.v34 = make([]v34Entry, c.HunkCount)
	for i := range c.v34 {
		b := raw[16*i:]
		e := v34Entry{
			offset: binary.BigEndian.Uint64(b[0:]),
			crc:    binary.BigEndian.Uint32(b[8:]),
			length: uint32(binary.BigEndian.Uint16(b[12:])) | uint32(b[14])<<16,
			flags:  b[15],
		}
		switch e.flags & v34TypeMask {
		case v34TypeCompressed, v34TypeUncompressed:
			n := e.length
			if e.flags&v34TypeMask == v34TypeUncompressed {
				n = c.HunkBytes
			}
			if e.offset+uint64(n) > uint64(st.Size()) {
				return fmt.Errorf("chd: hunk %d lies beyond the end of the file (truncated?)", i)
			}
		}
		c.v34[i] = e
	}
	// unit size, as MAME guesses it for old files: CD frames when the
	// metadata describes a CD, otherwise the hunk
	c.UnitBytes = c.HunkBytes
	if c.Kind() == "CD" {
		c.UnitBytes = cdFrameBytes
	}
	return nil
}

func (c *File) readHunkV34(st *decState, n uint32, dst []byte, depth int) error {
	dst = dst[:c.HunkBytes]
	e := c.v34[n]
	switch e.flags & v34TypeMask {
	case v34TypeCompressed:
		if c.v34Codec == v34CompNone {
			return fmt.Errorf("hunk %d: compressed entry in an uncompressed CHD", n)
		}
		if cap(st.comp) < int(e.length) {
			st.comp = make([]byte, e.length)
		}
		src := st.comp[:e.length]
		if _, err := c.f.ReadAt(src, int64(e.offset)); err != nil {
			return err
		}
		if err := st.inflate(src, dst); err != nil {
			return fmt.Errorf("hunk %d: %w", n, err)
		}
	case v34TypeUncompressed:
		if _, err := c.f.ReadAt(dst, int64(e.offset)); err != nil {
			return err
		}
	case v34TypeMini:
		if len(dst) < 8 {
			return fmt.Errorf("hunk %d: hunk too small", n)
		}
		binary.BigEndian.PutUint64(dst, e.offset)
		for i := 8; i < len(dst); i++ {
			dst[i] = dst[i-8]
		}
	case v34TypeSelf:
		// only an earlier hunk: a damaged map could otherwise loop forever
		if e.offset >= uint64(n) || depth > 16 {
			return fmt.Errorf("hunk %d: bad self reference", n)
		}
		return c.readHunkV34(st, uint32(e.offset), dst, depth+1)
	case v34TypeParent:
		return ErrParent
	default:
		return fmt.Errorf("chd: bad map entry type %d", e.flags&v34TypeMask)
	}
	if e.flags&v34FlagNoCRC == 0 && crc32.ChecksumIEEE(dst) != e.crc {
		return fmt.Errorf("hunk %d: %w", n, ErrCorrupt)
	}
	return nil
}

// parseCHCD reads the binary CD table of v3 files: track count, then 99
// tracks of six 32-bit fields (type, subtype, data size, subcode size,
// frames, padding frames), in the byte order of the machine that wrote it.
func parseCHCD(b []byte) []Track {
	if len(b) < 4 {
		return nil
	}
	var bo binary.ByteOrder = binary.LittleEndian
	n := bo.Uint32(b)
	if n == 0 || n > 99 {
		bo = binary.BigEndian
		n = bo.Uint32(b)
	}
	if n == 0 || n > 99 || len(b) < 4+int(n)*24 {
		return nil
	}
	types := []string{"MODE1", "MODE1_RAW", "MODE2", "MODE2_FORM1", "MODE2_FORM2", "MODE2_FORM_MIX", "MODE2_RAW", "AUDIO"}
	var out []Track
	frame := 0
	for i := 0; i < int(n); i++ {
		r := b[4+24*i:]
		typ := int(bo.Uint32(r))
		if typ >= len(types) {
			return nil
		}
		t := Track{Number: i + 1, Type: types[typ], Frames: int(bo.Uint32(r[16:])), StartFrm: frame}
		t.DataSize, t.DataOff = trackGeometry(t.Type)
		frame += t.Frames + int(bo.Uint32(r[20:]))
		out = append(out, t)
	}
	return out
}
