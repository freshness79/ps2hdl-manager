package disc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Layout says how the 2048 bytes of user data of each logical sector are stored
// in an image track. Whatever the layout, the HDD gets plain 2048-byte sectors.
type Layout struct {
	SectorSize int64  // bytes per stored sector
	DataOffset int64  // offset of the 2048 user bytes inside a stored sector
	Name       string // human readable
}

// Known layouts, in detection order.
var (
	Layout2048        = Layout{2048, 0, "2048-byte sectors (ISO)"}
	LayoutMode1Raw    = Layout{2352, 16, "raw MODE1/2352"}
	LayoutMode2Raw    = Layout{2352, 24, "raw MODE2/2352 (XA form 1)"}
	LayoutMode2_2336  = Layout{2336, 8, "MODE2/2336 (no sync/header)"}
	LayoutMode1RawSub = Layout{2448, 16, "raw MODE1/2352 + 96-byte subchannel"}
	LayoutMode2RawSub = Layout{2448, 24, "raw MODE2/2352 + 96-byte subchannel"}

	Layouts = []Layout{Layout2048, LayoutMode2Raw, LayoutMode1Raw, LayoutMode2_2336, LayoutMode2RawSub, LayoutMode1RawSub}
)

// Raw reports whether the layout is not plain 2048-byte sectors.
func (l Layout) Raw() bool { return l.SectorSize != 2048 }

// Mode is the CD mode (1 or 2) of raw layouts, 0 for plain sectors.
func (l Layout) Mode() int {
	switch l.DataOffset {
	case 16:
		return 1
	case 24, 8:
		return 2
	}
	return 0
}

// Sectors returns how many logical sectors fit in length bytes.
func (l Layout) Sectors(length int64) int64 { return length / l.SectorSize }

// LayoutFor returns the layout of a sector size and CD mode (1/2) declared by a
// descriptor file (CUE, MDS, CCD…); ok is false for unknown combinations.
func LayoutFor(sectorSize, mode int) (Layout, bool) {
	for _, l := range Layouts {
		if l.SectorSize == int64(sectorSize) && (l.Mode() == mode || l.SectorSize == 2048) {
			return l, true
		}
	}
	return Layout{}, false
}

// DetectLayout finds the layout by looking for the ISO9660 volume descriptor
// ("CD001" at logical sector 16) — or a UDF volume recognition sequence — with
// every known layout. The error lists what was tried and, when the data looks
// like something else (archive, compressed image…), what it is.
func DetectLayout(r io.ReaderAt) (Layout, error) {
	magic := make([]byte, 6)
	for _, l := range Layouts {
		if _, err := r.ReadAt(magic, 16*l.SectorSize+l.DataOffset); err == nil && string(magic[1:6]) == "CD001" {
			return l, nil
		}
	}
	if HasUDF(r) {
		return Layout2048, nil
	}
	return Layout{}, &LayoutError{Hint: MagicHint(r)}
}

// LayoutError: no file system found with any known sector layout.
type LayoutError struct {
	Hint string // what the data looks like instead ("this is a RAR archive…"), may be empty
}

func (e *LayoutError) Error() string {
	msg := "no ISO9660/UDF file system found with any known sector layout"
	if e.Hint != "" {
		msg += "; " + e.Hint
	}
	return msg
}

// Detail lists what was checked, for the activity log and diagnostics.
func (e *LayoutError) Detail() string {
	var sb strings.Builder
	sb.WriteString("Looked for the volume descriptor (CD001 / NSR0x) at sector 16 stored as:\n")
	for _, l := range Layouts {
		sb.WriteString("  - " + l.Name + "\n")
	}
	return sb.String()
}

// HasUDF reports whether a UDF volume recognition sequence (NSR02/NSR03) exists
// in the 2048-byte sectors 16..31.
func HasUDF(r io.ReaderAt) bool {
	b := make([]byte, 6)
	for s := int64(16); s < 32; s++ {
		if _, err := r.ReadAt(b, s*2048); err != nil {
			return false
		}
		if id := string(b[1:6]); id == "NSR02" || id == "NSR03" {
			return true
		}
	}
	return false
}

// MagicHint recognises common files that are not disc images (or need another
// plug-in) from their first bytes, to explain a failure.
func MagicHint(r io.ReaderAt) string {
	head := make([]byte, 64)
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	has := func(p string) bool { return bytes.HasPrefix(head, []byte(p)) }
	switch {
	case n == 0:
		return "the file is empty"
	case has("PK\x03\x04"):
		return "this is a ZIP archive: extract the disc image first"
	case has("Rar!\x1a\x07"):
		return "this is a RAR archive: extract the disc image first"
	case has("7z\xbc\xaf\x27\x1c"):
		return "this is a 7-Zip archive: extract the disc image first"
	case has("\x1f\x8b"):
		return "this is a gzip-compressed file: decompress it first"
	case has("\xfd7zXZ\x00"):
		return "this is an XZ-compressed file: decompress it first"
	case has("BZh"):
		return "this is a bzip2-compressed file: decompress it first"
	case has("CISO"):
		return "this is a CSO compressed image (rename it to .cso)"
	case has("ZISO"):
		return "this is a ZSO compressed image (rename it to .zso)"
	case has("MComprHD"):
		return "this is a CHD image (rename it to .chd)"
	case has("ECM\x00"):
		return "this is an ECM-encoded image: decode it with unecm first"
	case has("DAX\x00"):
		return "this is a DAX compressed image (PSP format, not supported)"
	case has("MEDIA DESCRIPTOR"):
		return "this is an Alcohol MDS descriptor: open the .mds/.mdf pair"
	case has("[CloneCD]"):
		return "this is a CloneCD descriptor: open the .ccd/.img pair"
	case has("\x7fPKG"), has("\x7fCNT"):
		return "this is a PlayStation Store package, not a disc image"
	case bytes.Contains(bytes.ToUpper(head), []byte("FILE ")) && bytes.Contains(bytes.ToUpper(head), []byte("TRACK")):
		return "this looks like a CUE sheet"
	}
	allZero := true
	for _, c := range head {
		if c != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return "the start of the file is empty (zeros): the image may be truncated or corrupted"
	}
	return ""
}

// sectorReaderFor returns a 2048-byte logical sector reader for a layout.
func sectorReaderFor(r io.ReaderAt, l Layout) *sectorReader {
	return &sectorReader{f: r, size: l.SectorSize, dataOff: l.DataOffset}
}

// WriteISO converts length bytes of r (stored with layout l) into a plain ISO
// with 2048-byte sectors at dst. progress receives 0..1 (may be nil).
func WriteISO(ctx context.Context, r io.ReaderAt, length int64, l Layout, dst string, progress func(float64)) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(dst)
		}
	}()
	w := bufioWriter(out)
	total := l.Sectors(length)
	if total == 0 {
		return fmt.Errorf("track too short: %d bytes", length)
	}
	const batch = 512
	buf := make([]byte, batch*l.SectorSize)
	for lba := int64(0); lba < total; lba += batch {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(batch, total-lba)
		got, err := r.ReadAt(buf[:n*l.SectorSize], lba*l.SectorSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading sector %d: %w", lba, err)
		}
		if int64(got) < n*l.SectorSize {
			return fmt.Errorf("image truncated at sector %d (expected %d sectors)", lba+int64(got)/l.SectorSize, total)
		}
		if !l.Raw() {
			_, err = w.Write(buf[:n*2048])
		} else {
			for i := int64(0); i < n && err == nil; i++ {
				o := i*l.SectorSize + l.DataOffset
				_, err = w.Write(buf[o : o+2048])
			}
		}
		if err != nil {
			return err
		}
		if progress != nil {
			progress(float64(lba+n) / float64(total))
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	ok = true
	return out.Close()
}

// Verify2048 checks that path is a plain ISO with 2048-byte sectors, the only
// thing written to the PS2 disk: size multiple of 2048 and a volume descriptor
// (ISO9660, or UDF) found with the 2048 layout.
func Verify2048(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size()%2048 != 0 {
		return fmt.Errorf("%d bytes is not a whole number of 2048-byte sectors", st.Size())
	}
	l, err := DetectLayout(f)
	if err != nil {
		return err
	}
	if l.Raw() {
		return fmt.Errorf("data is stored as %s, not as 2048-byte sectors", l.Name)
	}
	return nil
}
