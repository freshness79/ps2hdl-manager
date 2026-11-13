package chd

import (
	"bytes"
	"compress/flate"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz/lzma"
)

// Codec tags (CHD_CODEC_*).
const (
	codecNone = 0
	codecZlib = 0x7a6c6962 // zlib
	codecZstd = 0x7a737464 // zstd
	codecLZMA = 0x6c7a6d61 // lzma
	codecHuff = 0x68756666 // huff
	codecFLAC = 0x666c6163 // flac
	codecCDZl = 0x63647a6c // cdzl
	codecCDZs = 0x63647a73 // cdzs
	codecCDLz = 0x63646c7a // cdlz
	codecCDFl = 0x6364666c // cdfl
)

// Metadata tags.
const (
	TagCDTrack2 = 0x43485432 // CHT2
	TagCDTrack  = 0x43485452 // CHTR
	TagGDTrack  = 0x43484744 // CHGD
	TagDVD      = 0x44564420 // "DVD "
	TagCDOld    = 0x43484344 // CHCD: binary CD table of old (v3) files
)

const (
	compType0 = iota
	compType1
	compType2
	compType3
	compNone
	compSelf
	compParent
	compRLESmall
	compRLELarge
	compSelf0
	compSelf1
	compParentSelf
	compParent0
	compParent1
)

var (
	ErrNotCHD      = errors.New("chd: not a CHD file")
	ErrVersion     = errors.New("chd: only CHD v3, v4 and v5 are supported")
	ErrParent      = errors.New("chd: parent (diff) CHDs are not supported")
	ErrCorrupt     = errors.New("chd: data corrupted (CRC mismatch)")
	ErrUnsupported = errors.New("chd: unsupported codec")
)

// eccRegenerated counts sectors whose ECC was rebuilt (used by tests).
var eccRegenerated atomic.Int64

// File is an open CHD image (v3, v4 or v5).
type File struct {
	f            *os.File
	Version      int
	LogicalBytes uint64
	HunkBytes    uint32
	UnitBytes    uint32
	HunkCount    uint32
	Codecs       [4]uint32
	RawSHA1      [20]byte
	SHA1         [20]byte
	mapOffset    uint64
	metaOffset   uint64
	compressed   bool
	rawmap       []byte     // 12 bytes per hunk (decoded v5 map)
	v34          []v34Entry // map of v3/v4 files
	v34Codec     uint32     // v3/v4 header compression
	md5          [16]byte   // v3: MD5 of the raw data
	st           *decState
}

// decState holds per-goroutine scratch buffers and decoders.
type decState struct {
	comp  []byte
	cdbuf []byte
	zdec  *zstd.Decoder
	fl    io.ReadCloser
	flsrc *bytes.Reader
}

func (d *decState) close() {
	if d.zdec != nil {
		d.zdec.Close()
	}
}

// Open opens a CHD file (v3, v4 or v5) for reading.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	c := &File{f: f, st: &decState{}}
	if err := c.readHeader(); err != nil {
		f.Close()
		return nil, err
	}
	return c, nil
}

func (c *File) Close() error {
	c.st.close()
	return c.f.Close()
}

func (c *File) readHeader() error {
	h := make([]byte, 124)
	if _, err := c.f.ReadAt(h[:16], 0); err != nil {
		return ErrNotCHD
	}
	if string(h[:8]) != "MComprHD" {
		return ErrNotCHD
	}
	switch v := binary.BigEndian.Uint32(h[12:]); v {
	case 5:
		c.Version = 5
	case 3, 4:
		c.Version = int(v)
		return c.readHeaderV34()
	default:
		return fmt.Errorf("%w (file is v%d)", ErrVersion, v)
	}
	if _, err := c.f.ReadAt(h, 0); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(h[8:]) != 124 {
		return ErrNotCHD
	}
	for i := 0; i < 4; i++ {
		c.Codecs[i] = binary.BigEndian.Uint32(h[16+4*i:])
	}
	c.LogicalBytes = binary.BigEndian.Uint64(h[32:])
	c.mapOffset = binary.BigEndian.Uint64(h[40:])
	c.metaOffset = binary.BigEndian.Uint64(h[48:])
	c.HunkBytes = binary.BigEndian.Uint32(h[56:])
	c.UnitBytes = binary.BigEndian.Uint32(h[60:])
	copy(c.RawSHA1[:], h[64:84])
	copy(c.SHA1[:], h[84:104])
	var zero [20]byte
	if !bytes.Equal(h[104:124], zero[:]) {
		return ErrParent
	}
	if c.HunkBytes == 0 || c.UnitBytes == 0 {
		return ErrNotCHD
	}
	c.HunkCount = uint32((c.LogicalBytes + uint64(c.HunkBytes) - 1) / uint64(c.HunkBytes))
	c.compressed = c.Codecs[0] != codecNone
	return c.readMap()
}

func (c *File) readMap() error {
	if !c.compressed {
		raw := make([]byte, 4*int(c.HunkCount))
		if _, err := c.f.ReadAt(raw, int64(c.mapOffset)); err != nil {
			return err
		}
		c.rawmap = make([]byte, 12*int(c.HunkCount))
		for i := uint32(0); i < c.HunkCount; i++ {
			off := uint64(binary.BigEndian.Uint32(raw[4*i:])) * uint64(c.HunkBytes)
			e := c.rawmap[12*i:]
			e[0] = compNone
			put48(e[4:], off)
		}
		return nil
	}
	var hdr [16]byte
	if _, err := c.f.ReadAt(hdr[:], int64(c.mapOffset)); err != nil {
		return err
	}
	mapBytes := binary.BigEndian.Uint32(hdr[0:])
	firstOffs := get48(hdr[4:])
	mapCRC := binary.BigEndian.Uint16(hdr[10:])
	lengthBits, selfBits, parentBits := int(hdr[12]), int(hdr[13]), int(hdr[14])
	cm := make([]byte, mapBytes)
	if _, err := c.f.ReadAt(cm, int64(c.mapOffset)+16); err != nil {
		return err
	}
	br := newBitReader(cm)
	dec := newHuffDecoder(16, 8)
	if err := dec.importTreeRLE(br); err != nil {
		return err
	}
	c.rawmap = make([]byte, 12*int(c.HunkCount))
	var last uint8
	rep := 0
	for i := uint32(0); i < c.HunkCount; i++ {
		e := c.rawmap[12*i:]
		if rep > 0 {
			e[0] = last
			rep--
			continue
		}
		v := uint8(dec.decodeOne(br))
		switch v {
		case compRLESmall:
			e[0] = last
			rep = 2 + int(dec.decodeOne(br))
		case compRLELarge:
			e[0] = last
			rep = 2 + 16 + int(dec.decodeOne(br))<<4
			rep += int(dec.decodeOne(br))
		default:
			e[0] = v
			last = v
		}
	}
	cur := firstOffs
	var lastSelf uint64
	var lastParent uint64
	for i := uint32(0); i < c.HunkCount; i++ {
		e := c.rawmap[12*i:]
		offset := cur
		var length uint32
		var crc uint16
		switch e[0] {
		case compType0, compType1, compType2, compType3:
			length = br.read(lengthBits)
			cur += uint64(length)
			crc = uint16(br.read(16))
		case compNone:
			length = c.HunkBytes
			cur += uint64(length)
			crc = uint16(br.read(16))
		case compSelf:
			offset = br.read64(selfBits)
			lastSelf = offset
		case compParent:
			offset = br.read64(parentBits)
			lastParent = offset
		case compSelf1:
			lastSelf++
			fallthrough
		case compSelf0:
			e[0] = compSelf
			offset = lastSelf
		case compParentSelf:
			e[0] = compParent
			offset = uint64(i) * uint64(c.HunkBytes) / uint64(c.UnitBytes)
			lastParent = offset
		case compParent1:
			lastParent += uint64(c.HunkBytes / c.UnitBytes)
			fallthrough
		case compParent0:
			e[0] = compParent
			offset = lastParent
		}
		put24(e[1:], length)
		put48(e[4:], offset)
		binary.BigEndian.PutUint16(e[10:], crc)
	}
	if crc16(c.rawmap) != mapCRC {
		return fmt.Errorf("chd: map CRC mismatch")
	}
	return nil
}

// ReadHunk decompresses hunk n into dst (len >= HunkBytes). Not safe for concurrent use.
func (c *File) ReadHunk(n uint32, dst []byte) error { return c.readHunk(c.st, n, dst) }

func (c *File) readHunk(st *decState, n uint32, dst []byte) error {
	if n >= c.HunkCount {
		return io.EOF
	}
	if c.v34 != nil {
		return c.readHunkV34(st, n, dst, 0)
	}
	dst = dst[:c.HunkBytes]
	e := c.rawmap[12*n:]
	length := get24(e[1:])
	offset := get48(e[4:])
	crc := binary.BigEndian.Uint16(e[10:])
	switch e[0] {
	case compType0, compType1, compType2, compType3:
		if cap(st.comp) < int(length) {
			st.comp = make([]byte, length)
		}
		src := st.comp[:length]
		if _, err := c.f.ReadAt(src, int64(offset)); err != nil {
			return err
		}
		if err := c.decompress(st, c.Codecs[e[0]], src, dst); err != nil {
			return fmt.Errorf("hunk %d: %w", n, err)
		}
		if crc16(dst) != crc {
			return fmt.Errorf("hunk %d: %w", n, ErrCorrupt)
		}
	case compNone:
		if !c.compressed && offset == 0 {
			clear(dst)
			return nil
		}
		if _, err := c.f.ReadAt(dst, int64(offset)); err != nil {
			return err
		}
		if c.compressed && crc16(dst) != crc {
			return fmt.Errorf("hunk %d: %w", n, ErrCorrupt)
		}
	case compSelf:
		return c.readHunk(st, uint32(offset), dst)
	case compParent:
		return ErrParent
	default:
		return fmt.Errorf("chd: bad map entry type %d", e[0])
	}
	return nil
}

func (c *File) decompress(st *decState, codec uint32, src, dst []byte) error {
	switch codec {
	case codecZlib:
		return st.inflate(src, dst)
	case codecZstd:
		return st.unzstd(src, dst)
	case codecLZMA:
		return unlzma(src, dst, c.HunkBytes)
	case codecHuff:
		return huffDecompress(src, dst)
	case codecFLAC:
		if len(src) < 1 || (src[0] != 'L' && src[0] != 'B') {
			return ErrCorrupt
		}
		_, err := flacDecode(src[1:], dst, len(dst)/4, src[0] == 'B')
		return err
	case codecCDZl, codecCDLz, codecCDZs, codecCDFl:
		return c.cdDecompress(st, codec, src, dst)
	}
	return fmt.Errorf("%w %q", ErrUnsupported, tagString(codec))
}

func (c *File) cdDecompress(st *decState, codec uint32, src, dst []byte) error {
	frames := len(dst) / cdFrameSize
	sectors := frames * cdMaxSectorData
	sub := frames * cdMaxSubcodeData
	if cap(st.cdbuf) < sectors+sub {
		st.cdbuf = make([]byte, sectors+sub)
	}
	buf := st.cdbuf[:sectors+sub]
	if codec == codecCDFl {
		used, err := flacDecode(src, buf[:sectors], sectors/4, true)
		if err != nil {
			return err
		}
		if err := st.inflate(src[used:], buf[sectors:]); err != nil {
			return err
		}
	} else {
		complenBytes := 2
		if len(dst) >= 65536 {
			complenBytes = 3
		}
		eccBytes := (frames + 7) / 8
		hdr := eccBytes + complenBytes
		if len(src) < hdr {
			return ErrCorrupt
		}
		var base int
		if complenBytes > 2 {
			base = int(get24(src[eccBytes:]))
		} else {
			base = int(binary.BigEndian.Uint16(src[eccBytes:]))
		}
		if hdr+base > len(src) {
			return ErrCorrupt
		}
		bsrc, ssrc := src[hdr:hdr+base], src[hdr+base:]
		var err error
		switch codec {
		case codecCDZl:
			err = st.inflate(bsrc, buf[:sectors])
		case codecCDLz:
			err = unlzma(bsrc, buf[:sectors], uint32(sectors))
		case codecCDZs:
			err = st.unzstd(bsrc, buf[:sectors])
		}
		if err != nil {
			return err
		}
		if codec == codecCDZs {
			err = st.unzstd(ssrc, buf[sectors:])
		} else {
			err = st.inflate(ssrc, buf[sectors:])
		}
		if err != nil {
			return err
		}
		defer func() {
			for f := 0; f < frames; f++ {
				if src[f/8]&(1<<uint(f%8)) != 0 {
					s := dst[f*cdFrameSize : f*cdFrameSize+cdMaxSectorData]
					copy(s, cdSyncHeader[:])
					eccGenerate(s)
					eccRegenerated.Add(1)
				}
			}
		}()
	}
	for f := 0; f < frames; f++ {
		copy(dst[f*cdFrameSize:], buf[f*cdMaxSectorData:(f+1)*cdMaxSectorData])
		copy(dst[f*cdFrameSize+cdMaxSectorData:], buf[sectors+f*cdMaxSubcodeData:sectors+(f+1)*cdMaxSubcodeData])
	}
	return nil
}

func (st *decState) inflate(src, dst []byte) error {
	if st.fl == nil {
		st.flsrc = bytes.NewReader(src)
		st.fl = flate.NewReader(st.flsrc)
	} else {
		st.flsrc.Reset(src)
		if err := st.fl.(flate.Resetter).Reset(st.flsrc, nil); err != nil {
			return err
		}
	}
	_, err := io.ReadFull(st.fl, dst)
	return err
}

func (st *decState) unzstd(src, dst []byte) error {
	if st.zdec == nil {
		d, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return err
		}
		st.zdec = d
	}
	out, err := st.zdec.DecodeAll(src, dst[:0])
	if err != nil {
		return err
	}
	if len(out) != len(dst) || &out[0] != &dst[0] {
		if len(out) < len(dst) {
			return ErrCorrupt
		}
		copy(dst, out)
	}
	return nil
}

// unlzma decodes a raw LZMA stream as written by MAME (level 6, lc3 lp0 pb2,
// no header, no end marker) by prepending a classic .lzma header.
func unlzma(src, dst []byte, hunkBytes uint32) error {
	dict := uint32(1 << 12)
	for dict < hunkBytes && dict < 1<<26 {
		dict <<= 1
	}
	var hdr [13]byte
	hdr[0] = 0x5d // lc=3, lp=0, pb=2
	binary.LittleEndian.PutUint32(hdr[1:], dict)
	binary.LittleEndian.PutUint64(hdr[5:], uint64(len(dst)))
	// DictCap must be set: the default (8 MiB) would be allocated for every hunk
	r, err := lzma.ReaderConfig{DictCap: int(dict)}.NewReader(io.MultiReader(bytes.NewReader(hdr[:]), bytes.NewReader(src)))
	if err != nil {
		return err
	}
	_, err = io.ReadFull(r, dst)
	return err
}

// ---------------------------------------------------------------- metadata

// Metadata is one metadata entry.
type Metadata struct {
	Tag   uint32
	Flags uint8
	Data  []byte
}

// Metadata returns all metadata entries in file order.
func (c *File) Metadata() ([]Metadata, error) {
	var out []Metadata
	off := c.metaOffset
	for off != 0 && len(out) < 10000 {
		var h [16]byte
		if _, err := c.f.ReadAt(h[:], int64(off)); err != nil {
			return out, err
		}
		tag := binary.BigEndian.Uint32(h[0:])
		flags := h[4]
		length := get24(h[5:])
		next := binary.BigEndian.Uint64(h[8:])
		data := make([]byte, length)
		if _, err := c.f.ReadAt(data, int64(off)+16); err != nil {
			return out, err
		}
		out = append(out, Metadata{Tag: tag, Flags: flags, Data: data})
		off = next
	}
	return out, nil
}

// Kind returns "DVD", "CD" or "" (hard disk / other). A CHD without CD/DVD
// metadata whose data is a plain ISO 9660 image (some old tools stored DVDs
// as hard disk CHDs) counts as a DVD.
func (c *File) Kind() string {
	md, _ := c.Metadata()
	for _, m := range md {
		switch m.Tag {
		case TagDVD:
			return "DVD"
		case TagCDTrack2, TagCDTrack, TagGDTrack, TagCDOld:
			return "CD"
		}
	}
	if c.looksLikeISO() {
		return "DVD"
	}
	return ""
}

// looksLikeISO reports whether the logical data starts like an ISO 9660 image
// with 2048-byte sectors (primary volume descriptor at sector 16).
func (c *File) looksLikeISO() bool {
	const pvd = 16 * 2048
	if c.HunkBytes == 0 || c.LogicalBytes%2048 != 0 || c.LogicalBytes < pvd+2048 {
		return false
	}
	n := uint32(pvd / uint64(c.HunkBytes))
	buf := make([]byte, c.HunkBytes)
	st := &decState{}
	defer st.close()
	if c.readHunk(st, n, buf) != nil {
		return false
	}
	off := pvd % uint64(c.HunkBytes)
	return off+6 <= uint64(len(buf)) && string(buf[off:off+6]) == "\x01CD001"
}

// Track is a CD track as described by CHT2 metadata.
type Track struct {
	Number   int
	Type     string // MODE1, MODE1_RAW, MODE2_RAW, MODE2_FORM1, AUDIO, …
	Frames   int
	Pregap   int
	PGType   string
	Postgap  int
	DataSize int // bytes of user data stored per frame
	DataOff  int // offset of the 2048-byte payload inside the stored frame
	StartFrm int // first CHD frame of the track (after a stored pregap)
}

// Tracks parses CD track metadata.
func (c *File) Tracks() ([]Track, error) {
	md, err := c.Metadata()
	if err != nil {
		return nil, err
	}
	var tracks []Track
	for _, m := range md {
		if m.Tag == TagCDOld { // v3 binary table: positions included
			if t := parseCHCD(m.Data); t != nil {
				return t, nil
			}
			return nil, fmt.Errorf("chd: unreadable CD track table (CHCD)")
		}
		if m.Tag != TagCDTrack2 && m.Tag != TagCDTrack {
			continue
		}
		t := Track{}
		for _, f := range strings.Fields(strings.TrimRight(string(m.Data), "\x00")) {
			k, v, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			n, _ := strconv.Atoi(v)
			switch k {
			case "TRACK":
				t.Number = n
			case "TYPE":
				t.Type = v
			case "FRAMES":
				t.Frames = n
			case "PREGAP":
				t.Pregap = n
			case "PGTYPE":
				t.PGType = v
			case "POSTGAP":
				t.Postgap = n
			}
		}
		t.DataSize, t.DataOff = trackGeometry(t.Type)
		tracks = append(tracks, t)
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].Number < tracks[j].Number })
	frame := 0
	for i := range tracks {
		t := &tracks[i]
		start := frame
		if strings.HasPrefix(t.PGType, "V") {
			start += t.Pregap // pregap data is stored in the CHD
		}
		t.StartFrm = start
		frame += t.Frames
		frame += (4 - t.Frames%4) % 4 // CD_TRACK_PADDING
	}
	return tracks, nil
}

func trackGeometry(typ string) (size, off int) {
	switch typ {
	case "MODE1":
		return 2048, 0
	case "MODE1_RAW":
		return 2352, 16
	case "MODE2":
		return 2336, 8
	case "MODE2_FORM1":
		return 2048, 0
	case "MODE2_FORM2":
		return 2324, 0
	case "MODE2_FORM_MIX":
		return 2336, 8
	case "MODE2_RAW":
		return 2352, 24
	}
	return 2352, -1 // AUDIO
}

// ---------------------------------------------------------------- helpers

func get24(b []byte) uint32 { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }
func put24(b []byte, v uint32) {
	b[0], b[1], b[2] = byte(v>>16), byte(v>>8), byte(v)
}
func get48(b []byte) uint64 {
	return uint64(b[0])<<40 | uint64(b[1])<<32 | uint64(b[2])<<24 | uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
}
func put48(b []byte, v uint64) {
	for i := 0; i < 6; i++ {
		b[i] = byte(v >> (40 - 8*uint(i)))
	}
}

func tagString(t uint32) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], t)
	return string(b[:])
}

// ---------------------------------------------------------------- verification

// Verify decompresses every hunk and checks the raw and overall SHA-1 stored in the header.
func (c *File) Verify(progress func(float64)) error {
	h := sha1.New()
	var m5 hash.Hash
	if c.Version == 3 {
		m5 = md5.New() // v3 stores the MD5 of the data; its SHA-1 also covers metadata
	}
	remaining := c.LogicalBytes
	err := c.hunkStream(0, c.HunkCount, func(n uint32, data []byte) error {
		k := uint64(len(data))
		if k > remaining {
			k = remaining
		}
		h.Write(data[:k])
		if m5 != nil {
			m5.Write(data[:k])
		}
		remaining -= k
		if progress != nil && n%256 == 0 {
			progress(float64(n) / float64(c.HunkCount))
		}
		return nil
	})
	if err != nil {
		return err
	}
	var raw [20]byte
	copy(raw[:], h.Sum(nil))
	if m5 != nil {
		if !bytes.Equal(m5.Sum(nil), c.md5[:]) {
			return fmt.Errorf("chd: MD5 mismatch")
		}
		return nil
	}
	if raw != c.RawSHA1 {
		return fmt.Errorf("chd: raw SHA-1 mismatch")
	}
	md, err := c.Metadata()
	if err != nil {
		return err
	}
	if overallSHA1(raw, md) != c.SHA1 {
		return fmt.Errorf("chd: overall SHA-1 mismatch")
	}
	return nil
}

// overallSHA1 = SHA1(rawsha1 + sorted(tag+SHA1(data)) for checksummed metadata).
func overallSHA1(raw [20]byte, md []Metadata) [20]byte {
	var entries [][24]byte
	for _, m := range md {
		if m.Flags&1 == 0 {
			continue
		}
		var e [24]byte
		binary.BigEndian.PutUint32(e[:4], m.Tag)
		s := sha1.Sum(m.Data)
		copy(e[4:], s[:])
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i][:], entries[j][:]) < 0 })
	h := sha1.New()
	h.Write(raw[:])
	for _, e := range entries {
		h.Write(e[:])
	}
	var out [20]byte
	copy(out[:], h.Sum(nil))
	return out
}
