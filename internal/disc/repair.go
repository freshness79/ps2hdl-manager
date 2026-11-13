package disc

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

func bufioWriter(f *os.File) *bufio.Writer { return bufio.NewWriterSize(f, 4<<20) }

// RepairPathTableInPlace fixes the type-L path table of a (temporary) ISO in place.
func RepairPathTableInPlace(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	pvd := make([]byte, 2048)
	if _, err := f.ReadAt(pvd, 16*2048); err != nil {
		return err
	}
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return fmt.Errorf("primary volume descriptor not found")
	}
	rootLBA := binary.LittleEndian.Uint32(pvd[156+2 : 156+6])
	newPT := uint32((st.Size() + 2047) / 2048)
	pt := make([]byte, 2048)
	pt[0] = 1
	binary.LittleEndian.PutUint32(pt[2:6], rootLBA)
	binary.LittleEndian.PutUint16(pt[6:8], 1)
	if _, err := f.WriteAt(pt, int64(newPT)*2048); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(pvd[132:136], 10)
	binary.BigEndian.PutUint32(pvd[136:140], 10)
	binary.LittleEndian.PutUint32(pvd[140:144], newPT)
	binary.LittleEndian.PutUint32(pvd[144:148], 0)
	_, err = f.WriteAt(pvd, 16*2048)
	return err
}

// ExtractTrackISO copies the user data of the track stored in src between byte
// offsets start and end into a plain ISO with 2048-byte sectors. The sector
// layout is detected from the track itself.
func ExtractTrackISO(ctx context.Context, src string, start, end int64, dst string, progress func(float64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	sec := io.NewSectionReader(in, start, end-start)
	l, err := DetectLayout(sec)
	if err != nil {
		return err
	}
	return WriteISO(ctx, sec, end-start, l, dst, progress)
}
