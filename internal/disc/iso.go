package disc

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var SerialRe = regexp.MustCompile(`([A-Z]{4})[_\-]?(\d{3})\.?(\d{2})`)

// NormalizeSerial puts a serial in canonical form: "SLUS90001".
func NormalizeSerial(s string) string {
	m := SerialRe.FindStringSubmatch(strings.ToUpper(s))
	if m == nil {
		return ""
	}
	return m[1] + m[2] + m[3]
}

// StartupFromSerial turns "SLUS90001" into "SLUS_900.01".
func StartupFromSerial(n string) string {
	if len(n) != 9 {
		return n
	}
	return n[:4] + "_" + n[4:7] + "." + n[7:]
}

func binFromCue(cue string) (string, error) {
	f, err := os.Open(cue)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	re := regexp.MustCompile(`(?i)^\s*FILE\s+"?([^"]+?)"?\s+\w+\s*$`)
	for sc.Scan() {
		if m := re.FindStringSubmatch(sc.Text()); m != nil {
			p := m[1]
			if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(cue), p)
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("no FILE entry found in cue sheet")
}

// sectorReader reads 2048-byte logical sectors from ISO or raw (2352) images.
type sectorReader struct {
	f       io.ReaderAt
	size    int64 // physical sector size
	dataOff int64 // offset of the user data in the sector
}

func (s *sectorReader) read(lba int64, n int) ([]byte, error) {
	out := make([]byte, 0, n*2048)
	buf := make([]byte, 2048)
	for i := 0; i < n; i++ {
		if _, err := s.f.ReadAt(buf, (lba+int64(i))*s.size+s.dataOff); err != nil {
			return nil, err
		}
		out = append(out, buf...)
	}
	return out, nil
}

func detectFormat(f io.ReaderAt) (*sectorReader, error) {
	l, err := DetectLayout(f)
	if err != nil {
		return nil, err
	}
	return sectorReaderFor(f, l), nil
}

func readSystemCNF(f io.ReaderAt) (string, error) {
	sr, err := detectFormat(f)
	if err != nil {
		return "", err
	}
	pvd, err := sr.read(16, 1)
	if err != nil {
		return "", err
	}
	root := pvd[156:]
	lba := int64(binary.LittleEndian.Uint32(root[2:6]))
	size := int(binary.LittleEndian.Uint32(root[10:14]))
	nsec := (size + 2047) / 2048
	if nsec > 64 {
		nsec = 64
	}
	dir, err := sr.read(lba, nsec)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(dir); {
		l := int(dir[i])
		if l == 0 { // end of sector: go on with the next one
			i = (i/2048 + 1) * 2048
			continue
		}
		if i+l > len(dir) || l < 34 {
			break
		}
		rec := dir[i : i+l]
		nl := int(rec[32])
		if 33+nl <= len(rec) {
			name := strings.ToUpper(string(rec[33 : 33+nl]))
			if strings.HasPrefix(name, "SYSTEM.CNF") {
				flba := int64(binary.LittleEndian.Uint32(rec[2:6]))
				fsz := int(binary.LittleEndian.Uint32(rec[10:14]))
				if fsz > 4096 {
					fsz = 4096
				}
				data, err := sr.read(flba, (fsz+2047)/2048)
				if err != nil {
					return "", err
				}
				return parseSystemCNF(data[:fsz])
			}
		}
		i += l
	}
	return "", fmt.Errorf("SYSTEM.CNF not found")
}

func parseSystemCNF(b []byte) (string, error) {
	for _, line := range bytes.Split(b, []byte("\n")) {
		s := strings.ToUpper(string(line))
		if strings.HasPrefix(strings.TrimSpace(s), "BOOT") {
			if m := SerialRe.FindStringSubmatch(s); m != nil {
				return StartupFromSerial(m[1] + m[2] + m[3]), nil
			}
		}
	}
	return "", fmt.Errorf("BOOT2 not found in SYSTEM.CNF")
}

// ---------------------------------------------------------------- diagnostics & repair

// Diag describes how hdl_dump will see an image.
type Diag struct {
	Path        string
	Format      string // layout description ("2048-byte sectors (ISO)", "raw MODE2/2352"…)
	Layout      Layout
	Raw         bool
	RawMode     int  // 1 or 2 when Raw
	UDFOnly     bool // UDF file system without ISO9660 (hdl_dump and the PS2 need ISO9660)
	SystemID    string
	VolumeID    string
	BootLine    string
	Startup     string
	PathTableOK bool
	Issues      []string // human readable problems
	Err111      bool     // hdl_dump would fail with "bad ISOFS"
	Err112      bool     // hdl_dump would fail with "not a PlayStation CD/DVD" (bypassed by passing the startup ID)
}

// Inspect analyses an image the same way hdl_dump does and reports problems.
func Inspect(path string) (*Diag, error) {
	dataPath := path
	if strings.EqualFold(filepath.Ext(path), ".cue") {
		b, err := binFromCue(path)
		if err != nil {
			return nil, err
		}
		dataPath = b
	}
	f, err := os.Open(dataPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return InspectReader(path, f, strings.EqualFold(filepath.Ext(path), ".cue"))
}

// InspectReader analyses image data from any reader (e.g. the data track inside a
// container); name is used in messages, cue tells whether hdl_dump will get a CUE.
func InspectReader(name string, f io.ReaderAt, cue bool) (*Diag, error) {
	d := &Diag{Path: name}
	l, err := DetectLayout(f)
	if err != nil {
		d.Issues = append(d.Issues, "Not a readable disc image: "+err.Error()+".")
		d.Err111 = true
		return d, nil
	}
	sr := sectorReaderFor(f, l)
	d.Layout, d.Format, d.Raw, d.RawMode = l, l.Name, l.Raw(), l.Mode()
	pvd, err := sr.read(16, 1)
	if err != nil {
		return nil, err
	}
	if string(pvd[1:6]) != "CD001" { // UDF only
		d.UDFOnly, d.Err111 = true, true
		d.Format += ", UDF only"
		d.Issues = append(d.Issues, "UDF file system without ISO9660: hdl_dump and the PS2 need the ISO9660 file system that PS2 discs carry (UDF bridge). Rebuild the image as ISO9660 or UDF-bridge.")
		if cnf, err := udfReadFile(f, "SYSTEM.CNF"); err == nil {
			d.Startup, _ = parseSystemCNF(cnf)
			for _, line := range strings.Split(strings.ReplaceAll(string(cnf), "\r", ""), "\n") {
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "BOOT") {
					d.BootLine = strings.TrimSpace(line)
					break
				}
			}
		} else {
			d.Issues = append(d.Issues, "SYSTEM.CNF not readable in the UDF file system: "+err.Error()+".")
		}
		return d, nil
	}
	d.SystemID = strings.TrimRight(string(pvd[8:40]), " \x00")
	d.VolumeID = strings.TrimRight(string(pvd[40:72]), " \x00")

	if d.Raw {
		d.Issues = append(d.Issues, "Stored as "+l.Name+": converted to 2048-byte sectors before writing to the PS2 disk.")
	}
	_ = cue

	// path table check (hdl_dump finds the root directory through the type-L path table)
	rootLBA := binary.LittleEndian.Uint32(pvd[156+2 : 156+6])
	ptLBA := int64(binary.LittleEndian.Uint32(pvd[140:144]))
	d.PathTableOK = false
	if ptLBA > 16 {
		if pt, err := sr.read(ptLBA, 1); err == nil {
			if pt[0] == 1 && pt[8] == 0 && binary.LittleEndian.Uint32(pt[2:6]) == rootLBA {
				d.PathTableOK = true
			}
		}
	}
	if !d.PathTableOK {
		d.Err111 = true
		d.Issues = append(d.Issues, "Broken type-L path table in the volume descriptor (typical of rebuilt/patched ISOs): hdl_dump fails with error 111. Use “Fix” to create a repaired copy.")
	}

	if d.SystemID != "PLAYSTATION" {
		d.Err112 = true
		d.Issues = append(d.Issues, fmt.Sprintf("System ID is %q instead of \"PLAYSTATION\": hdl_dump reports error 112 unless the startup ID is passed (done automatically).", d.SystemID))
	}

	if cnf, err := readSystemCNFRaw(sr); err == nil {
		for _, line := range strings.Split(strings.ReplaceAll(string(cnf), "\r", ""), "\n") {
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "BOOT") {
				d.BootLine = strings.TrimSpace(line)
				break
			}
		}
		d.Startup, _ = parseSystemCNF(cnf)
		if d.BootLine != "" && !regexp.MustCompile(`^BOOT2\s*=\s*cdrom[01]:\\`).MatchString(d.BootLine) {
			d.Err112 = true
			d.Issues = append(d.Issues, "Non-standard BOOT2 line (\""+d.BootLine+"\"): hdl_dump reports error 112 unless the startup ID is passed (done automatically).")
		}
		if strings.HasPrefix(strings.ToUpper(d.BootLine), "BOOT ") || strings.HasPrefix(strings.ToUpper(d.BootLine), "BOOT=") {
			d.Issues = append(d.Issues, "SYSTEM.CNF has BOOT instead of BOOT2: this is a PlayStation 1 disc, not supported by HD Loader/OPL.")
		}
	} else {
		d.Err112 = true
		d.Issues = append(d.Issues, "SYSTEM.CNF not found in the root directory: enter the startup ID manually.")
	}
	if d.Startup == "" && d.Err112 {
		d.Issues = append(d.Issues, "Startup ID could not be detected: type it in the ID field (e.g. SLUS_123.45).")
	}
	return d, nil
}

func (d *Diag) String() string {
	var sb strings.Builder
	if d.UDFOnly {
		fmt.Fprintf(&sb, "File:        %s\nFormat:      %s\nBOOT line:   %s\nStartup ID:  %s\n\n", d.Path, d.Format, d.BootLine, d.Startup)
		for _, i := range d.Issues {
			sb.WriteString("• " + i + "\n")
		}
		return sb.String()
	}
	fmt.Fprintf(&sb, "File:        %s\nFormat:      %s\nSystem ID:   %q\nVolume ID:   %q\nBOOT line:   %s\nStartup ID:  %s\nPath table:  %v\n\n",
		d.Path, d.Format, d.SystemID, d.VolumeID, d.BootLine, d.Startup, map[bool]string{true: "OK", false: "BROKEN"}[d.PathTableOK])
	if len(d.Issues) == 0 {
		sb.WriteString("No problems found: hdl_dump should accept this image.\n")
	}
	for _, i := range d.Issues {
		sb.WriteString("• " + i + "\n")
	}
	return sb.String()
}

func readSystemCNFRaw(sr *sectorReader) ([]byte, error) {
	pvd, err := sr.read(16, 1)
	if err != nil {
		return nil, err
	}
	root := pvd[156:]
	lba := int64(binary.LittleEndian.Uint32(root[2:6]))
	size := int(binary.LittleEndian.Uint32(root[10:14]))
	nsec := (size + 2047) / 2048
	if nsec > 64 {
		nsec = 64
	}
	dir, err := sr.read(lba, nsec)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(dir); {
		l := int(dir[i])
		if l == 0 {
			i = (i/2048 + 1) * 2048
			continue
		}
		if i+l > len(dir) || l < 34 {
			break
		}
		rec := dir[i : i+l]
		nl := int(rec[32])
		if 33+nl <= len(rec) && strings.HasPrefix(strings.ToUpper(string(rec[33:33+nl])), "SYSTEM.CNF") {
			flba := int64(binary.LittleEndian.Uint32(rec[2:6]))
			fsz := int(binary.LittleEndian.Uint32(rec[10:14]))
			if fsz > 4096 {
				fsz = 4096
			}
			data, err := sr.read(flba, (fsz+2047)/2048)
			if err != nil {
				return nil, err
			}
			return data[:fsz], nil
		}
		i += l
	}
	return nil, fmt.Errorf("SYSTEM.CNF not found")
}

// RepairPathTable writes a repaired copy of a 2048-byte ISO: a valid one-entry
// type-L path table is appended as an extra sector and the volume descriptor is
// pointed at it. The original file is not modified. progress receives 0..1.
func RepairPathTable(src, dst string, progress func(float64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.Size()%2048 != 0 {
		return fmt.Errorf("image size is not a multiple of 2048: only plain ISO files can be repaired")
	}
	pvd := make([]byte, 2048)
	if _, err := in.ReadAt(pvd, 16*2048); err != nil {
		return err
	}
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return fmt.Errorf("primary volume descriptor not found")
	}
	rootLBA := binary.LittleEndian.Uint32(pvd[156+2 : 156+6])
	newPT := uint32(st.Size() / 2048)

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
	buf := make([]byte, 4<<20)
	var done int64
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if progress != nil {
				progress(float64(done) / float64(st.Size()))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	// path table: len_di=1, ext=0, extent(LE)=root, parent=1, id=0x00, pad
	pt := make([]byte, 2048)
	pt[0] = 1
	binary.LittleEndian.PutUint32(pt[2:6], rootLBA)
	binary.LittleEndian.PutUint16(pt[6:8], 1)
	if _, err := out.Write(pt); err != nil {
		return err
	}
	// patch PVD: path table size (both-endian) and L-table location
	binary.LittleEndian.PutUint32(pvd[132:136], 10)
	binary.BigEndian.PutUint32(pvd[136:140], 10)
	binary.LittleEndian.PutUint32(pvd[140:144], newPT)
	binary.LittleEndian.PutUint32(pvd[144:148], 0) // optional L table: none
	if _, err := out.WriteAt(pvd, 16*2048); err != nil {
		return err
	}
	ok = true
	return out.Close()
}
