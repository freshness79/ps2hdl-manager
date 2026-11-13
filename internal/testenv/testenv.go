// Package testenv provides helpers shared by the integration tests:
// the tools under test (REAL_DEV / REAL_SELF) and
// synthetic disc images built from a small reference ISO.
package testenv

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/freshness79/ps2hdl-manager/internal/testenv/sample"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
)

// ISOPath writes the sample PS2 image (see package sample) to a temporary
// file and returns its path.
func ISOPath(t testing.TB) string { return sample.Path(t, "sample.iso", nil) }

// Tools returns the built-in tools of the app binary REAL_SELF and the test
// disk REAL_DEV (an APA disk the tests may overwrite); skips otherwise.
func Tools(t *testing.T) (*tools.HDL, *tools.PFS, string) {
	t.Helper()
	dev := os.Getenv("REAL_DEV")
	if dev == "" {
		t.Skip("REAL_DEV not set")
	}
	self := os.Getenv("REAL_SELF")
	if self == "" {
		t.Skip("REAL_SELF (built app binary) not set")
	}
	return &tools.HDL{Path: self, Tool: tools.ToolHDL}, &tools.PFS{Path: self, Tool: tools.ToolPFS}, dev
}

func LoadISO(t *testing.T) []byte { return sample.ISO() }

// ToRaw converts 2048-byte sectors to raw 2352-byte ones (mode 1 or mode 2 form 1).
func ToRaw(iso []byte, mode int) []byte {
	var out bytes.Buffer
	for i := 0; i+2048 <= len(iso); i += 2048 {
		sec := make([]byte, 2352)
		copy(sec, []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
		sec[15] = byte(mode)
		off := 16
		if mode == 2 {
			off = 24
		}
		copy(sec[off:], iso[i:i+2048])
		out.Write(sec)
	}
	return out.Bytes()
}

func chunk(id string, body []byte) []byte {
	b := make([]byte, 8, 8+len(body))
	copy(b, id)
	binary.BigEndian.PutUint32(b[4:], uint32(len(body)))
	return append(b, body...)
}

// NRGv2DAO builds a Nero v2 disc-at-once image: 150-sector pregap + track + chunks.
func NRGv2DAO(track []byte, sectorSize int) []byte {
	var img bytes.Buffer
	img.Write(make([]byte, 150*sectorSize))
	start := int64(img.Len())
	img.Write(track)
	end := int64(img.Len())
	daox := make([]byte, 22+42)
	binary.BigEndian.PutUint32(daox[0:], uint32(len(daox)))
	daox[20], daox[21] = 1, 1
	e := daox[22:]
	binary.BigEndian.PutUint16(e[12:], uint16(sectorSize))
	binary.BigEndian.PutUint64(e[18:], 0)
	binary.BigEndian.PutUint64(e[26:], uint64(start))
	binary.BigEndian.PutUint64(e[34:], uint64(end))
	table := int64(img.Len())
	img.Write(chunk("CUEX", make([]byte, 32)))
	img.Write(chunk("DAOX", daox))
	img.Write(chunk("SINF", []byte{0, 0, 0, 1}))
	img.Write(chunk("END!", nil))
	f := make([]byte, 12)
	copy(f, "NER5")
	binary.BigEndian.PutUint64(f[4:], uint64(table))
	img.Write(f)
	return img.Bytes()
}

// NRGv1TAO builds a Nero v1 track-at-once image (ETNF chunk, NERO footer).
func NRGv1TAO(track []byte) []byte {
	var img bytes.Buffer
	img.Write(track)
	etnf := make([]byte, 20)
	binary.BigEndian.PutUint32(etnf[0:], 0)
	binary.BigEndian.PutUint32(etnf[4:], uint32(len(track)))
	table := int64(img.Len())
	img.Write(chunk("ETNF", etnf))
	img.Write(chunk("END!", nil))
	f := make([]byte, 8)
	copy(f, "NERO")
	binary.BigEndian.PutUint32(f[4:], uint32(table))
	img.Write(f)
	return img.Bytes()
}

func Write(t *testing.T, name string, b []byte) string {
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ToLayout stores 2048-byte sectors with a given sector size and user-data
// offset: 2352/16 (MODE1), 2352/24 (MODE2), 2336/8, 2448/16 or 2448/24 (+96-byte subchannel).
func ToLayout(iso []byte, size, off int) []byte {
	var out bytes.Buffer
	for i := 0; i+2048 <= len(iso); i += 2048 {
		sec := make([]byte, size)
		if size >= 2352 {
			copy(sec, []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
			sec[15] = byte(map[int]int{16: 1, 24: 2}[off])
		}
		copy(sec[off:], iso[i:i+2048])
		out.Write(sec)
	}
	return out.Bytes()
}

// CueSheet writes a CUE sheet next to the data files and returns its path.
func CueSheet(t *testing.T, dir, name, body string) string {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// MDS builds an Alcohol 120% v1.3 descriptor for one data track (plus optional
// audio tracks) stored in "<base>.mdf": mode 0xAA (MODE1) / 0xAB (MODE2),
// sectorSize 2048/2352/2448, data at startOffset, dataSectors long.
func MDS(mode byte, sectorSize int, startOffset int64, dataSectors int, audio int, dvd bool) []byte {
	ntracks := 1 + audio
	nblocks := ntracks + 3 // A0, A1, A2 lead-in points + tracks
	const hdr, sess = 0x58, 24
	trackOff := hdr + sess
	extraOff := trackOff + nblocks*80
	footerOff := extraOff + ntracks*8
	nameOff := footerOff + 16
	b := make([]byte, nameOff+6)
	copy(b, "MEDIA DESCRIPTOR")
	b[16], b[17] = 1, 3
	if dvd {
		binary.LittleEndian.PutUint16(b[0x12:], 0x10)
	}
	binary.LittleEndian.PutUint16(b[0x14:], 1)
	binary.LittleEndian.PutUint32(b[0x50:], hdr)
	s := b[hdr:]
	binary.LittleEndian.PutUint16(s[8:], 1)
	s[10], s[11] = byte(nblocks), 3
	binary.LittleEndian.PutUint16(s[12:], 1)
	binary.LittleEndian.PutUint16(s[14:], uint16(ntracks))
	binary.LittleEndian.PutUint32(s[20:], uint32(trackOff))
	for i, pt := range []byte{0xA0, 0xA1, 0xA2} {
		tb := b[trackOff+i*80:]
		tb[4] = pt
	}
	pos := startOffset
	for k := 0; k < ntracks; k++ {
		tb := b[trackOff+(3+k)*80:]
		size, n := sectorSize, dataSectors
		tb[0] = mode
		if k > 0 {
			tb[0], size, n = 0xA9, 2352, 75
		}
		if sectorSize == 2448 {
			tb[1] = 0x08
		}
		tb[4] = byte(k + 1)
		binary.LittleEndian.PutUint32(tb[12:], uint32(extraOff+k*8))
		binary.LittleEndian.PutUint16(tb[16:], uint16(size))
		binary.LittleEndian.PutUint64(tb[40:], uint64(pos))
		binary.LittleEndian.PutUint32(tb[48:], 1)
		binary.LittleEndian.PutUint32(tb[52:], uint32(footerOff))
		binary.LittleEndian.PutUint32(b[extraOff+k*8+4:], uint32(n))
		pos += int64(n * size)
	}
	binary.LittleEndian.PutUint32(b[footerOff:], uint32(nameOff))
	copy(b[nameOff:], "*.mdf")
	return b
}

// CCD writes a CloneCD descriptor: track 1 data (MODE=mode) at sector 0 plus
// optional audio tracks starting at the given sectors.
func CCD(mode int, audioAt ...int) string {
	var sb bytes.Buffer
	sb.WriteString("[CloneCD]\r\nVersion=3\r\n[Disc]\r\nTocEntries=4\r\nSessions=1\r\nDataTracksScrambled=0\r\nCDTextLength=0\r\n[Session 1]\r\nPreGapMode=2\r\nPreGapSubC=0\r\n")
	sb.WriteString("[Entry 0]\r\nSession=1\r\nPoint=0xa0\r\n")
	sb.WriteString("[TRACK 1]\r\nMODE=" + string(rune('0'+mode)) + "\r\nINDEX 1=0\r\n")
	for i, at := range audioAt {
		sb.WriteString("[TRACK " + string(rune('2'+i)) + "]\r\nMODE=0\r\nINDEX 0=" + itoa(at-150) + "\r\nINDEX 1=" + itoa(at) + "\r\n")
	}
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// CHDv4CD: see sample.CHDv4CD.
func CHDv4CD(iso []byte) []byte { return sample.CHDv4CD(iso) }
