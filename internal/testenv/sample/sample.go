// Package sample holds the small synthetic PS2 disc image used by the tests
// (26 sectors: SYSTEM.CNF with BOOT2 = cdrom0:\SLUS_999.01;1 and a 2-byte
// stand-in for the ELF; no game data) and broken variants of it. It has no
// dependencies, so every package can use it in tests.
package sample

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

//go:embed testdata/ps2-sample.iso.gz
var isoGz []byte

// Startup is the startup file of the sample image.
const Startup = "SLUS_999.01"

var (
	once sync.Once
	iso  []byte
)

// ISO returns a copy of the sample image (ISO 9660, 2048-byte sectors).
func ISO() []byte {
	once.Do(func() {
		r, err := gzip.NewReader(bytes.NewReader(isoGz))
		if err != nil {
			panic(err)
		}
		iso, err = io.ReadAll(r)
		if err != nil {
			panic(err)
		}
	})
	return append([]byte(nil), iso...)
}

// Path writes b (the sample image when nil) to a temporary file and returns it.
func Path(t testing.TB, name string, b []byte) string {
	t.Helper()
	if b == nil {
		b = ISO()
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const pvd = 16 * 2048

// BrokenPathTable has a wrong L path table position in the volume descriptor:
// what OPL reports as error 111.
func BrokenPathTable() []byte {
	b := ISO()
	b[pvd+140] = 0
	return b
}

// WrongSystemID has a system identifier other than PLAYSTATION (error 112).
func WrongSystemID() []byte {
	b := ISO()
	copy(b[pvd+8:pvd+40], "WIN32                           ")
	return b
}

// CHDv4CD returns a CHD v4 (the format of MAME 0.12x–0.145) holding iso as a
// MODE2_RAW CD track: 4 frames of 2352+96 bytes per hunk, deflated hunks with
// CRC32, CHT2 metadata. Checksums in the header are left zero.
func CHDv4CD(iso []byte) []byte {
	const frame, hunk = 2448, 4 * 2448
	n := len(iso) / 2048
	data := make([]byte, ((n+3)/4)*hunk)
	for i := 0; i < n; i++ {
		f := data[i*frame:]
		copy(f, []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
		f[15] = 2
		copy(f[24:], iso[i*2048:(i+1)*2048])
	}
	hunks := len(data) / hunk
	const hdr = 108
	base := hdr + 16*hunks + 16
	var mp, body bytes.Buffer
	for i := 0; i < hunks; i++ {
		h := data[i*hunk : (i+1)*hunk]
		var z bytes.Buffer
		w, _ := flate.NewWriter(&z, 6)
		w.Write(h)
		w.Close()
		var e [16]byte
		binary.BigEndian.PutUint64(e[0:], uint64(base+body.Len()))
		binary.BigEndian.PutUint32(e[8:], crc32.ChecksumIEEE(h))
		binary.BigEndian.PutUint16(e[12:], uint16(z.Len()))
		e[14], e[15] = byte(z.Len()>>16), 1
		mp.Write(e[:])
		body.Write(z.Bytes())
	}
	meta := []byte(fmt.Sprintf("TRACK:1 TYPE:MODE2_RAW SUBTYPE:NONE FRAMES:%d PREGAP:0 PGTYPE:MODE1 PGSUB:RW POSTGAP:0\x00", n))
	h := make([]byte, hdr)
	copy(h, "MComprHD")
	binary.BigEndian.PutUint32(h[8:], hdr)
	binary.BigEndian.PutUint32(h[12:], 4)
	binary.BigEndian.PutUint32(h[20:], 2)
	binary.BigEndian.PutUint32(h[24:], uint32(hunks))
	binary.BigEndian.PutUint64(h[28:], uint64(len(data)))
	binary.BigEndian.PutUint64(h[36:], uint64(base+body.Len()))
	binary.BigEndian.PutUint32(h[44:], hunk)
	var mh [16]byte
	binary.BigEndian.PutUint32(mh[0:], 0x43485432) // CHT2
	mh[4] = 1
	mh[5], mh[6], mh[7] = byte(len(meta)>>16), byte(len(meta)>>8), byte(len(meta))
	var out bytes.Buffer
	out.Write(h)
	out.Write(mp.Bytes())
	out.WriteString("EndOfListCookie\x00")
	out.Write(body.Bytes())
	out.Write(mh[:])
	out.Write(meta)
	return out.Bytes()
}
