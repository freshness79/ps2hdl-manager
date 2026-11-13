package chd

import "errors"

var errHuffman = errors.New("chd: invalid huffman data")

// huffDecoder is a canonical huffman decoder (MAME huffman_decoder<N, M>).
type huffDecoder struct {
	numCodes int
	maxBits  int
	numBits  []uint8
	bits     []uint32
	lookup   []uint32 // value<<5 | length
}

func newHuffDecoder(numCodes, maxBits int) *huffDecoder {
	return &huffDecoder{
		numCodes: numCodes, maxBits: maxBits,
		numBits: make([]uint8, numCodes), bits: make([]uint32, numCodes),
		lookup: make([]uint32, 1<<maxBits),
	}
}

func (h *huffDecoder) importTreeRLE(br *bitReader) error {
	numbits := 3
	switch {
	case h.maxBits >= 16:
		numbits = 5
	case h.maxBits >= 8:
		numbits = 4
	}
	cur := 0
	for cur < h.numCodes {
		nb := int(br.read(numbits))
		if nb != 1 {
			h.numBits[cur] = uint8(nb)
			cur++
			continue
		}
		nb = int(br.read(numbits))
		if nb == 1 {
			h.numBits[cur] = 1
			cur++
			continue
		}
		rep := int(br.read(numbits)) + 3
		for ; rep > 0; rep-- {
			if cur >= h.numCodes {
				return errHuffman
			}
			h.numBits[cur] = uint8(nb)
			cur++
		}
	}
	if cur != h.numCodes {
		return errHuffman
	}
	if err := h.assignCanonical(); err != nil {
		return err
	}
	h.buildLookup()
	if br.over {
		return errHuffman
	}
	return nil
}

func (h *huffDecoder) importTreeHuffman(br *bitReader) error {
	small := newHuffDecoder(24, 6)
	small.numBits[0] = uint8(br.read(3))
	start := int(br.read(3)) + 1
	count := 0
	for i := 1; i < 24; i++ {
		if i < start || count == 7 {
			small.numBits[i] = 0
		} else {
			count = int(br.read(3))
			if count == 7 {
				small.numBits[i] = 0
			} else {
				small.numBits[i] = uint8(count)
			}
		}
	}
	if err := small.assignCanonical(); err != nil {
		return err
	}
	small.buildLookup()

	temp := uint32(h.numCodes - 9)
	rlefullbits := 0
	for temp != 0 {
		temp >>= 1
		rlefullbits++
	}
	last := 0
	cur := 0
	for cur < h.numCodes {
		v := int(small.decodeOne(br))
		if v != 0 {
			last = v - 1
			h.numBits[cur] = uint8(last)
			cur++
			continue
		}
		cnt := int(br.read(3)) + 2
		if cnt == 7+2 {
			cnt += int(br.read(rlefullbits))
		}
		for ; cnt != 0 && cur < h.numCodes; cnt-- {
			h.numBits[cur] = uint8(last)
			cur++
		}
	}
	if cur != h.numCodes {
		return errHuffman
	}
	if err := h.assignCanonical(); err != nil {
		return err
	}
	h.buildLookup()
	if br.over {
		return errHuffman
	}
	return nil
}

func (h *huffDecoder) assignCanonical() error {
	var histo [33]uint32
	for _, nb := range h.numBits {
		if int(nb) > h.maxBits {
			return errHuffman
		}
		histo[nb]++
	}
	var cur uint32
	for l := 32; l > 0; l-- {
		next := (cur + histo[l]) >> 1
		if l != 1 && next*2 != cur+histo[l] {
			return errHuffman
		}
		histo[l] = cur
		cur = next
	}
	for i, nb := range h.numBits {
		if nb > 0 {
			h.bits[i] = histo[nb]
			histo[nb]++
		}
	}
	return nil
}

func (h *huffDecoder) buildLookup() {
	for i, nb := range h.numBits {
		if nb == 0 {
			continue
		}
		val := uint32(i)<<5 | uint32(nb)
		shift := h.maxBits - int(nb)
		start := h.bits[i] << uint(shift)
		end := (h.bits[i]+1)<<uint(shift) - 1
		for j := start; j <= end && int(j) < len(h.lookup); j++ {
			h.lookup[j] = val
		}
	}
}

func (h *huffDecoder) decodeOne(br *bitReader) uint32 {
	l := h.lookup[br.peek(h.maxBits)]
	br.remove(int(l & 0x1f))
	return l >> 5
}

// huffDecompress implements the "huff" CHD codec (8-bit huffman).
func huffDecompress(src, dst []byte) error {
	br := newBitReader(src)
	h := newHuffDecoder(256, 16)
	if err := h.importTreeHuffman(br); err != nil {
		return err
	}
	for i := range dst {
		dst[i] = byte(h.decodeOne(br))
	}
	if br.over {
		return errHuffman
	}
	return nil
}

// ---------------------------------------------------------------- map encoder (fixed-length codes)

// exportFixedTreeRLE writes an RLE tree where every one of numCodes symbols
// uses `length` bits (a complete canonical tree when numCodes == 1<<length).
func exportFixedTreeRLE(bw *bitWriter, numCodes, maxBits, length int) {
	numbits := 3
	switch {
	case maxBits >= 16:
		numbits = 5
	case maxBits >= 8:
		numbits = 4
	}
	remaining := numCodes
	for remaining > 0 {
		if length == 1 { // a literal 1 is escaped as 1,1
			bw.write(1, numbits)
			bw.write(1, numbits)
			remaining--
			continue
		}
		if remaining >= 3 {
			rep := remaining
			if max := (1 << uint(numbits)) - 1 + 3; rep > max {
				rep = max
			}
			bw.write(1, numbits)
			bw.write(uint64(length), numbits)
			bw.write(uint64(rep-3), numbits)
			remaining -= rep
			continue
		}
		bw.write(uint64(length), numbits)
		remaining--
	}
}
