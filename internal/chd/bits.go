// Package chd reads (and writes DVD) MAME CHD v5 images in pure Go.
// Format and algorithms follow MAME's src/lib/util (chd.cpp, chdcodec.cpp,
// huffman.cpp, cdrom.cpp, flac.cpp), BSD-3-Clause.
package chd

// bitReader reads MSB-first bit fields; reads past the end return zero bits
// (same behaviour as MAME's bitstream_in).
type bitReader struct {
	buf  []byte
	pos  int // bit position
	over bool
}

func newBitReader(b []byte) *bitReader { return &bitReader{buf: b} }

func (r *bitReader) peek(n int) uint32 {
	var v uint32
	p := r.pos
	for i := 0; i < n; i++ {
		v <<= 1
		byteIdx := p >> 3
		if byteIdx < len(r.buf) {
			v |= uint32(r.buf[byteIdx]>>(7-uint(p&7))) & 1
		}
		p++
	}
	return v
}

func (r *bitReader) remove(n int) {
	r.pos += n
	if r.pos > len(r.buf)*8 {
		r.over = true
	}
}

func (r *bitReader) read(n int) uint32 {
	if n == 0 {
		return 0
	}
	v := r.peek(n)
	r.remove(n)
	return v
}

// read64 reads up to 64 bits.
func (r *bitReader) read64(n int) uint64 {
	if n <= 32 {
		return uint64(r.read(n))
	}
	hi := uint64(r.read(n - 32))
	return hi<<32 | uint64(r.read(32))
}

func (r *bitReader) alignByte() { r.pos = (r.pos + 7) &^ 7 }

// bytePos returns the number of whole bytes consumed.
func (r *bitReader) bytePos() int { return (r.pos + 7) >> 3 }

// bitWriter writes MSB-first bit fields.
type bitWriter struct {
	buf   []byte
	accum uint64
	nbits int
}

func (w *bitWriter) write(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		w.accum = w.accum<<1 | (v>>uint(i))&1
		w.nbits++
		if w.nbits == 8 {
			w.buf = append(w.buf, byte(w.accum))
			w.accum, w.nbits = 0, 0
		}
	}
}

func (w *bitWriter) flush() []byte {
	if w.nbits > 0 {
		w.buf = append(w.buf, byte(w.accum<<(8-uint(w.nbits))))
		w.accum, w.nbits = 0, 0
	}
	return w.buf
}

// crc16 is MAME's util::crc16 (CRC-CCITT, poly 0x1021, init 0xffff).
func crc16(data []byte) uint16 {
	crc := uint16(0xffff)
	for _, b := range data {
		crc = crc<<8 ^ crc16Table[byte(crc>>8)^b]
	}
	return crc
}

var crc16Table = func() (t [256]uint16) {
	for i := 0; i < 256; i++ {
		c := uint16(i) << 8
		for j := 0; j < 8; j++ {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x1021
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return
}()

// ---------------------------------------------------------------- CD ECC

const (
	cdFrameSize      = 2448
	cdMaxSectorData  = 2352
	cdMaxSubcodeData = 96
	cdSyncBytes      = 12
	cdModeOffset     = 0x00f
	eccPOffset       = 0x81c
	eccPNum          = 86
	eccPComp         = 24
	eccQOffset       = eccPOffset + 2*eccPNum
	eccQNum          = 52
	eccQComp         = 43
)

var cdSyncHeader = [12]byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

func eccSourceByte(sector []byte, offset uint16) byte {
	if sector[cdModeOffset] == 2 && offset < 4 {
		return 0
	}
	return sector[cdSyncBytes+int(offset)]
}

func eccComputeBytes(sector []byte, row []uint16) (byte, byte) {
	var v1, v2 byte
	for _, off := range row {
		s := eccSourceByte(sector, off)
		v1 ^= s
		v2 ^= s
		v1 = ecclow[v1]
	}
	v1 = ecchigh[ecclow[v1]^v2]
	v2 ^= v1
	return v1, v2
}

// eccGenerate writes the P and Q parity of a raw 2352-byte sector.
func eccGenerate(sector []byte) {
	for b := 0; b < eccPNum; b++ {
		sector[eccPOffset+b], sector[eccPOffset+eccPNum+b] = eccComputeBytes(sector, poffsets[b][:])
	}
	for b := 0; b < eccQNum; b++ {
		sector[eccQOffset+b], sector[eccQOffset+eccQNum+b] = eccComputeBytes(sector, qoffsets[b][:])
	}
}

// eccVerify reports whether the sector's parity is already correct.
func eccVerify(sector []byte) bool {
	for b := 0; b < eccPNum; b++ {
		v1, v2 := eccComputeBytes(sector, poffsets[b][:])
		if sector[eccPOffset+b] != v1 || sector[eccPOffset+eccPNum+b] != v2 {
			return false
		}
	}
	for b := 0; b < eccQNum; b++ {
		v1, v2 := eccComputeBytes(sector, qoffsets[b][:])
		if sector[eccQOffset+b] != v1 || sector[eccQOffset+eccQNum+b] != v2 {
			return false
		}
	}
	return true
}
