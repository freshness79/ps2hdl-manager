package disc

// Minimal read-only UDF (ECMA-167 / OSTA UDF 1.02–2.60) reader: enough to find a
// file in the root directory of a 2048-byte-sector image (SYSTEM.CNF).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const udfSector = 2048

type udfVolume struct {
	r         io.ReaderAt
	partStart int64 // absolute sector of partition 0
}

func (v *udfVolume) sector(abs int64) ([]byte, error) {
	b := make([]byte, udfSector)
	if _, err := v.r.ReadAt(b, abs*udfSector); err != nil {
		return nil, err
	}
	return b, nil
}

func tagID(b []byte) uint16 { return binary.LittleEndian.Uint16(b[0:2]) }

func openUDF(r io.ReaderAt) (*udfVolume, int64, error) {
	avdp := make([]byte, udfSector)
	if _, err := r.ReadAt(avdp, 256*udfSector); err != nil || tagID(avdp) != 2 {
		return nil, 0, errors.New("UDF anchor volume descriptor (sector 256) not found")
	}
	vdsLen := int64(binary.LittleEndian.Uint32(avdp[16:20]))
	vdsLoc := int64(binary.LittleEndian.Uint32(avdp[20:24]))
	v := &udfVolume{r: r, partStart: -1}
	fsdLBN := int64(-1)
	for i := int64(0); i < vdsLen/udfSector && i < 64; i++ {
		d, err := v.sector(vdsLoc + i)
		if err != nil {
			return nil, 0, err
		}
		switch tagID(d) {
		case 5: // partition descriptor
			if v.partStart < 0 {
				v.partStart = int64(binary.LittleEndian.Uint32(d[188:192]))
			}
		case 6: // logical volume descriptor: file set descriptor location
			if bs := binary.LittleEndian.Uint32(d[212:216]); bs != udfSector {
				return nil, 0, fmt.Errorf("UDF logical block size %d not supported", bs)
			}
			fsdLBN = int64(binary.LittleEndian.Uint32(d[252:256]))
		case 8: // terminating descriptor
			i = vdsLen
		}
	}
	if v.partStart < 0 || fsdLBN < 0 {
		return nil, 0, errors.New("UDF partition or logical volume descriptor missing")
	}
	fsd, err := v.sector(v.partStart + fsdLBN)
	if err != nil || tagID(fsd) != 256 {
		return nil, 0, errors.New("UDF file set descriptor not found")
	}
	root := int64(binary.LittleEndian.Uint32(fsd[404:408]))
	return v, root, nil
}

// readEntry reads the data described by the (extended) file entry at lbn.
func (v *udfVolume) readEntry(lbn int64, max int64) ([]byte, error) {
	fe, err := v.sector(v.partStart + lbn)
	if err != nil {
		return nil, err
	}
	var lEA, lAD, adOff int
	switch tagID(fe) {
	case 261: // file entry
		lEA, lAD, adOff = int(binary.LittleEndian.Uint32(fe[168:172])), int(binary.LittleEndian.Uint32(fe[172:176])), 176
	case 266: // extended file entry
		lEA, lAD, adOff = int(binary.LittleEndian.Uint32(fe[208:212])), int(binary.LittleEndian.Uint32(fe[212:216])), 216
	default:
		return nil, fmt.Errorf("UDF file entry expected at block %d (tag %d)", lbn, tagID(fe))
	}
	size := int64(binary.LittleEndian.Uint64(fe[56:64]))
	if size > max {
		size = max
	}
	adOff += lEA
	if adOff+lAD > udfSector {
		return nil, errors.New("UDF allocation descriptors out of range")
	}
	ads := fe[adOff : adOff+lAD]
	out := make([]byte, 0, size)
	switch binary.LittleEndian.Uint16(fe[34:36]) & 7 {
	case 3: // data embedded in the entry
		if int64(len(ads)) < size {
			size = int64(len(ads))
		}
		return append(out, ads[:size]...), nil
	case 0, 1: // short_ad (8 bytes) / long_ad (16 bytes)
		step := 8
		if binary.LittleEndian.Uint16(fe[34:36])&7 == 1 {
			step = 16
		}
		for i := 0; i+step <= len(ads) && int64(len(out)) < size; i += step {
			l := int64(binary.LittleEndian.Uint32(ads[i:]) & 0x3fffffff)
			pos := int64(binary.LittleEndian.Uint32(ads[i+4:]))
			if l == 0 {
				break
			}
			buf := make([]byte, min(l, size-int64(len(out))))
			if _, err := v.r.ReadAt(buf, (v.partStart+pos)*udfSector); err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			out = append(out, buf...)
		}
		return out, nil
	}
	return nil, errors.New("UDF allocation type not supported")
}

// udfReadFile returns a file of the root directory (case-insensitive name).
func udfReadFile(r io.ReaderAt, name string) ([]byte, error) {
	v, root, err := openUDF(r)
	if err != nil {
		return nil, err
	}
	dir, err := v.readEntry(root, 1<<20)
	if err != nil {
		return nil, err
	}
	for i := 0; i+38 <= len(dir); {
		if tagID(dir[i:]) != 257 { // file identifier descriptor
			break
		}
		lFI, lIU := int(dir[i+19]), int(binary.LittleEndian.Uint16(dir[i+36:i+38]))
		icb := int64(binary.LittleEndian.Uint32(dir[i+24 : i+28]))
		idStart := i + 38 + lIU
		if idStart+lFI > len(dir) {
			break
		}
		if fname := udfName(dir[idStart : idStart+lFI]); strings.EqualFold(fname, name) || strings.EqualFold(strings.TrimSuffix(fname, ";1"), name) {
			return v.readEntry(icb, 64<<10)
		}
		i += (38 + lIU + lFI + 3) &^ 3
	}
	return nil, fmt.Errorf("%s not found in the UDF root directory", name)
}

// udfName decodes an OSTA compressed unicode identifier (8- or 16-bit).
func udfName(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case 8:
		return string(b[1:])
	case 16:
		var sb strings.Builder
		for i := 1; i+1 < len(b); i += 2 {
			sb.WriteRune(rune(binary.BigEndian.Uint16(b[i:])))
		}
		return sb.String()
	}
	return ""
}
