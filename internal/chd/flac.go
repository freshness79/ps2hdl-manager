package chd

import (
	"encoding/binary"
	"errors"
)

var errFLAC = errors.New("chd: invalid FLAC data")

// flacDecode decodes raw FLAC frames (no stream header, as stored by CHD: 2 channels,
// 16-bit, the given nominal block size) until samples per channel are produced.
// Interleaved 16-bit samples are written to dst, big- or little-endian.
// It returns the number of bytes of src consumed (MAME flac_decoder::finish()).
func flacDecode(src []byte, dst []byte, samples int, bigEndian bool) (int, error) {
	br := newBitReader(src)
	out := 0
	var ch [2][]int32
	for out < samples {
		if br.bytePos() >= len(src) {
			return 0, errFLAC
		}
		n, err := flacFrame(br, &ch)
		if err != nil {
			return 0, err
		}
		for i := 0; i < n && out < samples; i++ {
			for c := 0; c < 2; c++ {
				v := uint16(int16(ch[c][i]))
				p := (out*2 + c) * 2
				if bigEndian {
					binary.BigEndian.PutUint16(dst[p:], v)
				} else {
					binary.LittleEndian.PutUint16(dst[p:], v)
				}
			}
			out++
		}
	}
	return br.bytePos(), nil
}

func flacFrame(br *bitReader, ch *[2][]int32) (int, error) {
	if br.read(14) != 0x3ffe {
		return 0, errFLAC
	}
	br.read(1) // reserved
	br.read(1) // blocking strategy
	bsCode := br.read(4)
	srCode := br.read(4)
	chanAssign := br.read(4)
	ssCode := br.read(3)
	br.read(1)
	// UTF-8 coded frame/sample number
	first := br.read(8)
	extra := 0
	for m := uint32(0x80); first&m != 0 && m > 1; m >>= 1 {
		extra++
	}
	if extra > 0 {
		extra--
	}
	for i := 0; i < extra; i++ {
		br.read(8)
	}
	var bs int
	switch {
	case bsCode == 1:
		bs = 192
	case bsCode >= 2 && bsCode <= 5:
		bs = 576 << (bsCode - 2)
	case bsCode == 6:
		bs = int(br.read(8)) + 1
	case bsCode == 7:
		bs = int(br.read(16)) + 1
	case bsCode >= 8:
		bs = 256 << (bsCode - 8)
	default:
		return 0, errFLAC
	}
	switch srCode {
	case 12:
		br.read(8)
	case 13, 14:
		br.read(16)
	}
	bps := 16
	switch ssCode {
	case 1:
		bps = 8
	case 2:
		bps = 12
	case 4:
		bps = 16
	case 5:
		bps = 20
	case 6:
		bps = 24
	}
	br.read(8) // CRC-8
	nch := 2
	if chanAssign < 8 {
		nch = int(chanAssign) + 1
	}
	if nch != 2 {
		return 0, errFLAC
	}
	for c := 0; c < 2; c++ {
		if cap(ch[c]) < bs {
			ch[c] = make([]int32, bs)
		}
		ch[c] = ch[c][:bs]
		sbps := bps
		if (chanAssign == 8 && c == 1) || (chanAssign == 9 && c == 0) || (chanAssign == 10 && c == 1) {
			sbps++ // side channel
		}
		if err := flacSubframe(br, ch[c], sbps); err != nil {
			return 0, err
		}
	}
	l, r := ch[0], ch[1]
	switch chanAssign {
	case 8: // left/side
		for i := range l {
			r[i] = l[i] - r[i]
		}
	case 9: // side/right
		for i := range l {
			l[i] = l[i] + r[i]
		}
	case 10: // mid/side
		for i := range l {
			mid, side := l[i], r[i]
			mid = mid<<1 | side&1
			l[i] = (mid + side) >> 1
			r[i] = (mid - side) >> 1
		}
	}
	br.alignByte()
	br.read(16) // CRC-16
	if br.over {
		return 0, errFLAC
	}
	return bs, nil
}

func signExtend(v uint32, n int) int32 {
	s := 32 - uint(n)
	return int32(v<<s) >> s
}

func flacSubframe(br *bitReader, out []int32, bps int) error {
	br.read(1)
	typ := br.read(6)
	wasted := 0
	if br.read(1) == 1 {
		wasted = 1
		for br.read(1) == 0 {
			wasted++
			if br.over {
				return errFLAC
			}
		}
		bps -= wasted
	}
	switch {
	case typ == 0: // CONSTANT
		v := signExtend(br.read(bps), bps)
		for i := range out {
			out[i] = v
		}
	case typ == 1: // VERBATIM
		for i := range out {
			out[i] = signExtend(br.read(bps), bps)
		}
	case typ >= 8 && typ <= 12: // FIXED
		order := int(typ - 8)
		for i := 0; i < order; i++ {
			out[i] = signExtend(br.read(bps), bps)
		}
		if err := flacResidual(br, out, order); err != nil {
			return err
		}
		for i := order; i < len(out); i++ {
			var p int32
			switch order {
			case 1:
				p = out[i-1]
			case 2:
				p = 2*out[i-1] - out[i-2]
			case 3:
				p = 3*out[i-1] - 3*out[i-2] + out[i-3]
			case 4:
				p = 4*out[i-1] - 6*out[i-2] + 4*out[i-3] - out[i-4]
			}
			out[i] += p
		}
	case typ >= 32: // LPC
		order := int(typ-32) + 1
		for i := 0; i < order; i++ {
			out[i] = signExtend(br.read(bps), bps)
		}
		prec := int(br.read(4)) + 1
		if prec == 16 {
			return errFLAC
		}
		shift := signExtend(br.read(5), 5)
		coefs := make([]int32, order)
		for i := range coefs {
			coefs[i] = signExtend(br.read(prec), prec)
		}
		if err := flacResidual(br, out, order); err != nil {
			return err
		}
		for i := order; i < len(out); i++ {
			var sum int64
			for j, c := range coefs {
				sum += int64(c) * int64(out[i-j-1])
			}
			if shift >= 0 {
				out[i] += int32(sum >> uint(shift))
			} else {
				out[i] += int32(sum << uint(-shift))
			}
		}
	default:
		return errFLAC
	}
	if wasted > 0 {
		for i := range out {
			out[i] <<= uint(wasted)
		}
	}
	return nil
}

func flacResidual(br *bitReader, out []int32, order int) error {
	method := br.read(2)
	if method > 1 {
		return errFLAC
	}
	paramBits, escape := 4, uint32(15)
	if method == 1 {
		paramBits, escape = 5, 31
	}
	partOrder := br.read(4)
	parts := 1 << partOrder
	bs := len(out)
	idx := order
	for p := 0; p < parts; p++ {
		n := bs >> partOrder
		if p == 0 {
			n -= order
		}
		if n < 0 || idx+n > bs {
			return errFLAC
		}
		param := br.read(paramBits)
		if param == escape {
			nb := int(br.read(5))
			for i := 0; i < n; i++ {
				if nb == 0 {
					out[idx] = 0
				} else {
					out[idx] = signExtend(br.read(nb), nb)
				}
				idx++
			}
			continue
		}
		for i := 0; i < n; i++ {
			q := uint32(0)
			for br.read(1) == 0 {
				q++
				if br.over {
					return errFLAC
				}
			}
			u := q<<param | br.read(int(param))
			out[idx] = int32(u>>1) ^ -int32(u&1)
			idx++
		}
	}
	return nil
}
