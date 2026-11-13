package chd

import (
	"bytes"
	"compress/flate"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// v34Opts describes a synthetic CHD v3/v4 written the way MAME 0.12x–0.145 did.
type v34Opts struct {
	version   int
	hunkBytes int
	data      []byte // logical data
	meta      []Metadata
	noCRC     map[int]bool // hunks written without CRC
	corrupt   int          // hunk whose stored CRC is wrong (-1: none)
}

// buildV34 writes the CHD and returns its bytes. Hunk kinds are chosen to cover
// every entry type: all-pattern hunks become "mini", repeats "self", hunks that
// do not shrink are stored, the rest deflated.
func buildV34(t *testing.T, o v34Opts) []byte {
	t.Helper()
	hb := o.hunkBytes
	n := (len(o.data) + hb - 1) / hb
	hdrSize := v4HeaderSize
	if o.version == 3 {
		hdrSize = v3HeaderSize
	}
	type ent struct {
		off    uint64
		crc    uint32
		length uint32
		typ    uint8
	}
	ents := make([]ent, n)
	var body bytes.Buffer
	base := uint64(hdrSize + 16*n + 16)
	seen := map[string]int{}
	for i := 0; i < n; i++ {
		h := make([]byte, hb)
		copy(h, o.data[i*hb:])
		crc := crc32.ChecksumIEEE(h)
		if i == o.corrupt {
			crc ^= 1
		}
		e := ent{crc: crc}
		isMini := true
		for j := 8; j < hb; j++ {
			if h[j] != h[j-8] {
				isMini = false
				break
			}
		}
		if prev, ok := seen[string(h)]; ok && !isMini {
			e.typ, e.off = v34TypeSelf, uint64(prev)
		} else if isMini {
			e.typ, e.off = v34TypeMini, binary.BigEndian.Uint64(h)
		} else {
			seen[string(h)] = i
			var z bytes.Buffer
			w, _ := flate.NewWriter(&z, 9)
			w.Write(h)
			w.Close()
			e.off = base + uint64(body.Len())
			if z.Len() < hb {
				e.typ, e.length = v34TypeCompressed, uint32(z.Len())
				body.Write(z.Bytes())
			} else {
				e.typ, e.length = v34TypeUncompressed, uint32(hb)
				body.Write(h)
			}
		}
		ents[i] = e
	}
	// metadata chain after the hunks
	metaOff := base + uint64(body.Len())
	var meta bytes.Buffer
	for i, m := range o.meta {
		var h [16]byte
		binary.BigEndian.PutUint32(h[0:], m.Tag)
		h[4] = m.Flags
		put24(h[5:], uint32(len(m.Data)))
		if i+1 < len(o.meta) {
			binary.BigEndian.PutUint64(h[8:], metaOff+uint64(meta.Len()+16+len(m.Data)))
		}
		meta.Write(h[:])
		meta.Write(m.Data)
	}
	if len(o.meta) == 0 {
		metaOff = 0
	}
	raw := sha1.Sum(o.data)
	var rawA [20]byte
	copy(rawA[:], raw[:])
	overall := overallSHA1(rawA, o.meta)

	hdr := make([]byte, hdrSize)
	copy(hdr, "MComprHD")
	binary.BigEndian.PutUint32(hdr[8:], uint32(hdrSize))
	binary.BigEndian.PutUint32(hdr[12:], uint32(o.version))
	binary.BigEndian.PutUint32(hdr[20:], v34CompZlibPlus)
	binary.BigEndian.PutUint32(hdr[24:], uint32(n))
	binary.BigEndian.PutUint64(hdr[28:], uint64(len(o.data)))
	binary.BigEndian.PutUint64(hdr[36:], metaOff)
	if o.version == 3 {
		m := md5.Sum(o.data)
		copy(hdr[44:], m[:])
		binary.BigEndian.PutUint32(hdr[76:], uint32(hb))
		copy(hdr[80:], overall[:])
	} else {
		binary.BigEndian.PutUint32(hdr[44:], uint32(hb))
		copy(hdr[48:], overall[:])
		copy(hdr[88:], raw[:])
	}
	var out bytes.Buffer
	out.Write(hdr)
	for i, e := range ents {
		var b [16]byte
		binary.BigEndian.PutUint64(b[0:], e.off)
		binary.BigEndian.PutUint32(b[8:], e.crc)
		binary.BigEndian.PutUint16(b[12:], uint16(e.length))
		b[14] = byte(e.length >> 16)
		b[15] = e.typ
		if o.noCRC[i] {
			b[15] |= v34FlagNoCRC
		}
		out.Write(b[:])
	}
	out.WriteString("EndOfListCookie\x00")
	out.Write(body.Bytes())
	out.Write(meta.Bytes())
	return out.Bytes()
}

// testPayload: 2048-byte sectors with an ISO 9660 PVD at sector 16, random
// data, zero runs and repeated sectors (to get every hunk type).
func testPayload(sectors int) []byte {
	r := rand.New(rand.NewSource(7))
	b := make([]byte, sectors*2048)
	r.Read(b)
	for s := 20; s < 40 && s < sectors; s++ { // zero run → mini hunks
		clear(b[s*2048 : (s+1)*2048])
	}
	for s := 60; s < 80 && s+40 < sectors; s++ { // repeats → self hunks
		copy(b[(s+40)*2048:], b[s*2048:(s+1)*2048])
	}
	copy(b[16*2048:], "\x01CD001\x01")
	return b
}

// rawMode2 turns 2048-byte sectors into stored CD frames: MODE2 form 1 sector
// (sync, header, subheader, data, EDC/ECC left zero) + 96 subcode bytes.
func rawMode2(iso []byte) []byte {
	n := len(iso) / 2048
	out := make([]byte, n*cdFrameBytes)
	for i := 0; i < n; i++ {
		f := out[i*cdFrameBytes:]
		copy(f, cdSyncHeader[:])
		f[15] = 2
		copy(f[24:], iso[i*2048:(i+1)*2048])
	}
	return out
}

func writeTemp(t *testing.T, name string, b []byte) string {
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if d := os.Getenv("CHD_V34_DUMP"); d != "" { // for checking with libchdr
		_ = os.WriteFile(filepath.Join(d, name), b, 0o644)
	}
	return p
}

func TestV4CD(t *testing.T) {
	iso := testPayload(203)                                          // odd count: last hunk partly padding
	frames := append(rawMode2(iso), make([]byte, 9*cdFrameBytes)...) // silent lead-out: mini hunks
	md := []Metadata{{Tag: TagCDTrack2, Flags: 1,
		Data: []byte("TRACK:1 TYPE:MODE2_RAW SUBTYPE:RW_RAW FRAMES:203 PREGAP:0 PGTYPE:MODE1 PGSUB:RW POSTGAP:0\x00")}}
	p := writeTemp(t, "v4cd.chd", buildV34(t, v34Opts{version: 4, hunkBytes: 4 * cdFrameBytes, data: frames, meta: md, corrupt: -1,
		noCRC: map[int]bool{3: true}}))
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Version != 4 || c.Kind() != "CD" || c.UnitBytes != cdFrameBytes {
		t.Fatalf("v%d kind %q unit %d", c.Version, c.Kind(), c.UnitBytes)
	}
	if err := c.Verify(nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := c.WriteISO(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), iso) {
		t.Fatalf("ISO differs (%d vs %d bytes)", buf.Len(), len(iso))
	}
	types := map[uint8]int{}
	for _, e := range c.v34 {
		types[e.flags&v34TypeMask]++
	}
	if types[v34TypeCompressed] == 0 || types[v34TypeMini] == 0 || types[v34TypeSelf] == 0 {
		t.Fatalf("not every hunk type exercised: %v", types)
	}
}

func TestV3CDOldMetadata(t *testing.T) {
	iso := testPayload(120)
	frames := rawMode2(iso)
	chcd := make([]byte, 4+99*24)
	binary.LittleEndian.PutUint32(chcd, 1)
	binary.LittleEndian.PutUint32(chcd[4:], 6) // MODE2_RAW
	binary.LittleEndian.PutUint32(chcd[4+8:], 2352)
	binary.LittleEndian.PutUint32(chcd[4+16:], 120)
	p := writeTemp(t, "v3cd.chd", buildV34(t, v34Opts{version: 3, hunkBytes: 8 * cdFrameBytes, data: frames, corrupt: -1,
		meta: []Metadata{{Tag: TagCDOld, Data: chcd}}}))
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Version != 3 || c.Kind() != "CD" {
		t.Fatal(c.Version, c.Kind())
	}
	if err := c.Verify(nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := c.WriteISO(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), iso) {
		t.Fatal("ISO differs")
	}
}

// A DVD stored as a hard-disk CHD (no CD/DVD metadata) is recognised by its
// ISO 9660 volume descriptor.
func TestV4PlainISO(t *testing.T) {
	iso := testPayload(300)
	p := writeTemp(t, "v4iso.chd", buildV34(t, v34Opts{version: 4, hunkBytes: 8192, data: iso, corrupt: -1,
		meta: []Metadata{{Tag: 0x47444444, Flags: 1, Data: []byte("CYLS:1,HEADS:1,SECS:1200,BPS:512\x00")}}}))
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Kind() != "DVD" {
		t.Fatal("kind", c.Kind())
	}
	var buf bytes.Buffer
	if err := c.WriteISO(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), iso) {
		t.Fatal("ISO differs")
	}
	stored := 0
	for _, e := range c.v34 {
		if e.flags&v34TypeMask == v34TypeUncompressed {
			stored++
		}
	}
	if stored == 0 {
		t.Fatal("no stored hunk exercised")
	}
}

func TestV34Damage(t *testing.T) {
	iso := testPayload(100)
	md := []Metadata{{Tag: TagCDTrack2, Flags: 1, Data: []byte("TRACK:1 TYPE:MODE2_RAW SUBTYPE:NONE FRAMES:100 PREGAP:0 PGTYPE:MODE1 PGSUB:RW POSTGAP:0\x00")}}
	good := buildV34(t, v34Opts{version: 4, hunkBytes: 4 * cdFrameBytes, data: rawMode2(iso), meta: md, corrupt: 0})
	c, err := Open(writeTemp(t, "bad.chd", good))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := c.WriteISO(&buf, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatal("CRC error not detected:", err)
	}
	c.Close()
	// truncated file and missing end marker are refused at open
	ok := buildV34(t, v34Opts{version: 4, hunkBytes: 4 * cdFrameBytes, data: rawMode2(iso), meta: md, corrupt: -1})
	if _, err := Open(writeTemp(t, "short.chd", ok[:len(ok)/2])); err == nil {
		t.Fatal("truncated file accepted")
	}
	broken := append([]byte(nil), ok...)
	copy(broken[v4HeaderSize+16*((100+3)/4):], "XXXXXXXXXXXXXXX")
	if _, err := Open(writeTemp(t, "cookie.chd", broken)); err == nil || !strings.Contains(err.Error(), "end marker") {
		t.Fatal("bad map accepted:", err)
	}
	// unsupported versions still say so
	v2 := append([]byte(nil), ok...)
	binary.BigEndian.PutUint32(v2[12:], 2)
	if _, err := Open(writeTemp(t, "v2.chd", v2)); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
	}
}

// With libchdr's reader (CHD_V34_DUMP=dir CHD_REF=path/to/dump) the synthetic
// files must decode to the same bytes: checks the layout against an
// independent implementation.
func TestV34AgainstLibchdr(t *testing.T) {
	ref, dir := os.Getenv("CHD_REF"), os.Getenv("CHD_V34_DUMP")
	if ref == "" || dir == "" {
		t.Skip("CHD_REF / CHD_V34_DUMP not set")
	}
	for _, name := range []string{"v4cd.chd", "v3cd.chd", "v4iso.chd"} {
		p := filepath.Join(dir, name)
		out := p + ".ref"
		b, err := exec.Command(ref, p, out).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: libchdr: %v %s", name, err, b)
		}
		t.Logf("%s: %s", name, strings.TrimSpace(string(b)))
		want, _ := os.ReadFile(out)
		c, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		buf := make([]byte, c.HunkBytes)
		left := c.LogicalBytes
		for i := uint32(0); i < c.HunkCount; i++ {
			if err := c.ReadHunk(i, buf); err != nil {
				t.Fatal(name, i, err)
			}
			k := min(uint64(len(buf)), left)
			got.Write(buf[:k])
			left -= k
		}
		c.Close()
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("%s: differs from libchdr (%d vs %d bytes)", name, got.Len(), len(want))
		}
	}
}
