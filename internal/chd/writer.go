package chd

import (
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
	"os"
	"runtime"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// WriteOptions configures CreateDVD.
type WriteOptions struct {
	// UseZstd also tries zstd for each hunk (≈1% smaller, ~2x slower; readable only by
	// MAME/chdman ≥ 0.256 and recent libchdr). Default: zlib only, readable everywhere.
	UseZstd bool
	// HunkBytes defaults to 16384 (8 sectors): with zlib this gives files smaller than
	// chdman's default LZMA/4 KB hunks, at a fraction of the time.
	HunkBytes uint32
}

// CreateDVD compresses a 2048-byte-sector ISO into a DVD CHD v5 at dst.
func CreateDVD(isoPath, dst string, opt WriteOptions, progress func(float64)) error {
	in, err := os.Open(isoPath)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.Size()%2048 != 0 {
		return fmt.Errorf("chd: %s is not a multiple of 2048 bytes", isoPath)
	}
	hunkBytes := opt.HunkBytes
	if hunkBytes == 0 {
		hunkBytes = 16384
	}
	if hunkBytes%2048 != 0 {
		return fmt.Errorf("chd: hunk size must be a multiple of 2048")
	}
	codecs := [4]uint32{codecZlib}
	if opt.UseZstd {
		codecs = [4]uint32{codecZstd, codecZlib}
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			out.Close()
			os.Remove(dst)
		}
	}()
	w := &countWriter{w: bufio.NewWriterSize(out, 4<<20)}
	if _, err := w.Write(make([]byte, 124)); err != nil { // header placeholder
		return err
	}
	logical := uint64(st.Size())
	hunks := uint32((logical + uint64(hunkBytes) - 1) / uint64(hunkBytes))
	firstOffs := w.n

	type entry struct {
		typ    uint8
		length uint32
		offset uint64
		crc    uint16
	}
	entries := make([]entry, hunks)
	seen := make(map[[20]byte]uint32)
	rawHash := sha1.New()

	// parallel compression, in-order writing
	type job struct {
		n    uint32
		data []byte
		comp []byte
		typ  uint8
		done chan struct{}
	}
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	jobs := make(chan *job, workers*4)
	order := make(chan *job, workers*4)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enc := newHunkCompressor(codecs)
			defer enc.close()
			for j := range jobs {
				j.typ, j.comp = enc.compress(j.data)
				close(j.done)
			}
		}()
	}
	readErr := make(chan error, 1)
	go func() {
		defer close(jobs)
		defer close(order)
		for n := uint32(0); n < hunks; n++ {
			buf := make([]byte, hunkBytes)
			k, err := io.ReadFull(in, buf)
			if err != nil && err != io.ErrUnexpectedEOF {
				readErr <- err
				return
			}
			_ = k // short last hunk stays zero-padded
			j := &job{n: n, data: buf, done: make(chan struct{})}
			order <- j
			jobs <- j
		}
		readErr <- nil
	}()
	var werr error
	remaining := logical
	for j := range order {
		<-j.done
		if werr != nil {
			continue
		}
		k := uint64(hunkBytes)
		if k > remaining {
			k = remaining
		}
		rawHash.Write(j.data[:k])
		remaining -= k
		sum := sha1.Sum(j.data)
		if first, dup := seen[sum]; dup {
			entries[j.n] = entry{typ: compSelf, offset: uint64(first)}
		} else {
			seen[sum] = j.n
			e := entry{typ: j.typ, offset: w.n, crc: crc16(j.data)}
			if j.typ == compNone {
				e.length = hunkBytes
				_, werr = w.Write(j.data)
			} else {
				e.length = uint32(len(j.comp))
				_, werr = w.Write(j.comp)
			}
			entries[j.n] = e
		}
		if progress != nil && j.n%512 == 0 {
			progress(float64(j.n) / float64(hunks))
		}
	}
	wg.Wait()
	if err := <-readErr; err != nil {
		return err
	}
	if werr != nil {
		return werr
	}

	// ---- map
	raw := make([]byte, 12*int(hunks))
	var maxLen uint32
	var maxSelf uint64
	for i, e := range entries {
		r := raw[12*i:]
		r[0] = e.typ
		put24(r[1:], e.length)
		put48(r[4:], e.offset)
		binary.BigEndian.PutUint16(r[10:], e.crc)
		if e.typ <= compType3 && e.length > maxLen {
			maxLen = e.length
		}
		if e.typ == compSelf && e.offset > maxSelf {
			maxSelf = e.offset
		}
	}
	lengthBits := bits.Len32(maxLen)
	selfBits := bits.Len64(maxSelf)
	bw := &bitWriter{}
	exportFixedTreeRLE(bw, 16, 8, 4) // all 16 map symbols coded with 4 bits
	for _, e := range entries {
		bw.write(uint64(e.typ), 4)
	}
	for _, e := range entries {
		switch e.typ {
		case compType0, compType1, compType2, compType3:
			bw.write(uint64(e.length), lengthBits)
			bw.write(uint64(e.crc), 16)
		case compNone:
			bw.write(uint64(e.crc), 16)
		case compSelf:
			bw.write(e.offset, selfBits)
		}
	}
	cmap := bw.flush()
	mapOffset := w.n
	var mh [16]byte
	binary.BigEndian.PutUint32(mh[0:], uint32(len(cmap)))
	put48(mh[4:], firstOffs)
	binary.BigEndian.PutUint16(mh[10:], crc16(raw))
	mh[12], mh[13], mh[14] = byte(lengthBits), byte(selfBits), 0
	if _, err := w.Write(mh[:]); err != nil {
		return err
	}
	if _, err := w.Write(cmap); err != nil {
		return err
	}

	// ---- metadata: "DVD " tag, checksummed, empty string (1 NUL byte) like chdman
	metaOffset := w.n
	md := Metadata{Tag: TagDVD, Flags: 1, Data: []byte{0}}
	var mdh [16]byte
	binary.BigEndian.PutUint32(mdh[0:], md.Tag)
	mdh[4] = md.Flags
	put24(mdh[5:], uint32(len(md.Data)))
	if _, err := w.Write(mdh[:]); err != nil {
		return err
	}
	if _, err := w.Write(md.Data); err != nil {
		return err
	}
	if err := w.w.(*bufio.Writer).Flush(); err != nil {
		return err
	}

	// ---- header
	var rawSHA [20]byte
	copy(rawSHA[:], rawHash.Sum(nil))
	all := overallSHA1(rawSHA, []Metadata{md})
	h := make([]byte, 124)
	copy(h, "MComprHD")
	binary.BigEndian.PutUint32(h[8:], 124)
	binary.BigEndian.PutUint32(h[12:], 5)
	for i, cdc := range codecs {
		binary.BigEndian.PutUint32(h[16+4*i:], cdc)
	}
	binary.BigEndian.PutUint64(h[32:], logical)
	binary.BigEndian.PutUint64(h[40:], mapOffset)
	binary.BigEndian.PutUint64(h[48:], metaOffset)
	binary.BigEndian.PutUint32(h[56:], hunkBytes)
	binary.BigEndian.PutUint32(h[60:], 2048)
	copy(h[64:], rawSHA[:])
	copy(h[84:], all[:])
	if _, err := out.WriteAt(h, 0); err != nil {
		return err
	}
	if progress != nil {
		progress(1)
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

type countWriter struct {
	w io.Writer
	n uint64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += uint64(n)
	return n, err
}

// hunkCompressor tries each configured codec and keeps the smallest result.
type hunkCompressor struct {
	codecs [4]uint32
	zenc   *zstd.Encoder
	fw     *flate.Writer
	fbuf   bytes.Buffer
}

func newHunkCompressor(codecs [4]uint32) *hunkCompressor {
	h := &hunkCompressor{codecs: codecs}
	for _, c := range codecs {
		switch c {
		case codecZstd:
			h.zenc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(false))
		case codecZlib:
			h.fw, _ = flate.NewWriter(&h.fbuf, flate.BestCompression)
		}
	}
	return h
}

func (h *hunkCompressor) close() {
	if h.zenc != nil {
		h.zenc.Close()
	}
}

func (h *hunkCompressor) compress(data []byte) (uint8, []byte) {
	best := []byte(nil)
	bestTyp := uint8(compNone)
	for i, c := range h.codecs {
		var out []byte
		switch c {
		case codecZstd:
			out = h.zenc.EncodeAll(data, nil)
		case codecZlib:
			h.fbuf.Reset()
			h.fw.Reset(&h.fbuf)
			h.fw.Write(data)
			h.fw.Close()
			out = append([]byte(nil), h.fbuf.Bytes()...)
		default:
			continue
		}
		if len(out) < len(data) && (best == nil || len(out) < len(best)) {
			best, bestTyp = out, uint8(i)
		}
	}
	return bestTyp, best
}
