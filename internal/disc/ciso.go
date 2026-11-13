package disc

// Compressed ISO images: CSO (CISO v1 deflate, v2 deflate+LZ4) and ZSO (LZ4).

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// CISO is a random-access reader of the decompressed image.
type CISO struct {
	f         *os.File
	Magic     string // "CISO" or "ZISO"
	Version   int
	Size      int64 // decompressed size
	BlockSize int64
	align     uint
	index     []uint32

	mu       sync.Mutex
	cacheIdx int64
	cache    []byte
}

// OpenCISO opens a .cso/.zso file.
func OpenCISO(path string) (*CISO, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	c, err := newCISO(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return c, nil
}

func newCISO(f *os.File) (*CISO, error) {
	h := make([]byte, 24)
	if _, err := io.ReadFull(f, h); err != nil {
		return nil, fmt.Errorf("header too short: %w", err)
	}
	c := &CISO{f: f, Magic: string(h[:4]), cacheIdx: -1}
	if c.Magic != "CISO" && c.Magic != "ZISO" {
		msg := fmt.Sprintf("not a CSO/ZSO image (magic %q)", h[:4])
		if hint := MagicHint(f); hint != "" {
			msg += "; " + hint
		}
		return nil, errors.New(msg)
	}
	c.Size = int64(binary.LittleEndian.Uint64(h[8:16]))
	c.BlockSize = int64(binary.LittleEndian.Uint32(h[16:20]))
	c.Version, c.align = int(h[20]), uint(h[21])
	if c.BlockSize < 2048 || c.BlockSize > 1<<20 || c.BlockSize&(c.BlockSize-1) != 0 {
		return nil, fmt.Errorf("unsupported block size %d", c.BlockSize)
	}
	if c.Size <= 0 || c.Size > 16<<30 {
		return nil, fmt.Errorf("implausible image size %d bytes", c.Size)
	}
	if c.Version > 2 {
		return nil, fmt.Errorf("%s version %d not supported", c.Magic, c.Version)
	}
	n := (c.Size+c.BlockSize-1)/c.BlockSize + 1
	raw := make([]byte, n*4)
	if _, err := f.ReadAt(raw, 24); err != nil {
		return nil, fmt.Errorf("block index truncated: %w", err)
	}
	c.index = make([]uint32, n)
	for i := range c.index {
		c.index[i] = binary.LittleEndian.Uint32(raw[i*4:])
	}
	return c, nil
}

// Close closes the file.
func (c *CISO) Close() error { return c.f.Close() }

// Name describes the container for messages.
func (c *CISO) Name() string {
	if c.Magic == "ZISO" {
		return "ZSO (LZ4)"
	}
	if c.Version == 2 {
		return "CSO v2 (deflate/LZ4)"
	}
	return "CSO v1 (deflate)"
}

// block returns decompressed block i.
func (c *CISO) block(i int64) ([]byte, error) {
	want := c.BlockSize
	if rest := c.Size - i*c.BlockSize; rest < want {
		want = rest
	}
	cur, next := c.index[i], c.index[i+1]
	pos := int64(cur&0x7fffffff) << c.align
	end := int64(next&0x7fffffff) << c.align
	if end < pos {
		return nil, fmt.Errorf("block %d: corrupted index", i)
	}
	comp := make([]byte, end-pos)
	if _, err := c.f.ReadAt(comp, pos); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("block %d: %w", i, err)
	}
	high := cur&0x80000000 != 0
	plain, lz4 := false, false
	switch {
	case c.Magic == "ZISO":
		plain, lz4 = high, !high
	case c.Version == 2:
		plain, lz4 = int64(len(comp)) >= c.BlockSize, high
	default:
		plain = high
	}
	if plain {
		if int64(len(comp)) < want {
			return nil, fmt.Errorf("block %d: stored block truncated", i)
		}
		return comp[:want], nil
	}
	if lz4 {
		out, err := lz4Block(comp, int(want))
		if err != nil {
			return nil, fmt.Errorf("block %d: LZ4: %w", i, err)
		}
		return out, nil
	}
	out := make([]byte, want)
	fr := flate.NewReader(bytes.NewReader(comp))
	defer fr.Close()
	if _, err := io.ReadFull(fr, out); err != nil {
		return nil, fmt.Errorf("block %d: deflate: %w", i, err)
	}
	return out, nil
}

// ReadAt implements io.ReaderAt over the decompressed image.
func (c *CISO) ReadAt(p []byte, off int64) (int, error) {
	if off >= c.Size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off < c.Size {
		bi := off / c.BlockSize
		c.mu.Lock()
		if c.cacheIdx != bi {
			b, err := c.block(bi)
			if err != nil {
				c.mu.Unlock()
				return n, err
			}
			c.cache, c.cacheIdx = b, bi
		}
		k := copy(p[n:], c.cache[off-bi*c.BlockSize:])
		c.mu.Unlock()
		n += k
		off += int64(k)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// lz4Block decodes one LZ4 block (raw format, no frame) producing want bytes.
func lz4Block(src []byte, want int) ([]byte, error) {
	dst := make([]byte, 0, want)
	i := 0
	for i < len(src) && len(dst) < want {
		token := src[i]
		i++
		lit := int(token >> 4)
		if lit == 15 {
			for i < len(src) {
				b := src[i]
				i++
				lit += int(b)
				if b != 255 {
					break
				}
			}
		}
		if i+lit > len(src) {
			return nil, errors.New("literals beyond input")
		}
		dst = append(dst, src[i:i+lit]...)
		i += lit
		if len(dst) >= want || i >= len(src) {
			break // last sequence: literals only
		}
		if i+2 > len(src) {
			return nil, errors.New("truncated match offset")
		}
		offset := int(binary.LittleEndian.Uint16(src[i:]))
		i += 2
		if offset == 0 || offset > len(dst) {
			return nil, fmt.Errorf("invalid match offset %d", offset)
		}
		ml := int(token & 15)
		if ml == 15 {
			for i < len(src) {
				b := src[i]
				i++
				ml += int(b)
				if b != 255 {
					break
				}
			}
		}
		ml += 4
		start := len(dst) - offset
		for k := 0; k < ml; k++ { // byte by byte: overlapping copies are allowed
			dst = append(dst, dst[start+k])
		}
	}
	if len(dst) < want {
		return nil, fmt.Errorf("decoded %d bytes, expected %d", len(dst), want)
	}
	return dst[:want], nil
}
