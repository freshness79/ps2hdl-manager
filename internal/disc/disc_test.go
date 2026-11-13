package disc

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freshness79/ps2hdl-manager/internal/testenv/sample"
)

func TestSerial(t *testing.T) {
	for _, s := range []string{"SLUS_900.01", "SLUS-90001", "slus90001", "cdrom0:\\SLUS_900.01;1"} {
		if NormalizeSerial(s) != "SLUS90001" {
			t.Errorf("%s -> %s", s, NormalizeSerial(s))
		}
	}
	if StartupFromSerial("SLUS90001") != "SLUS_900.01" {
		t.Error("StartupFromSerial")
	}
}

func TestISO(t *testing.T) {
	for _, p := range []string{os.Getenv("TEST_ISO"), os.Getenv("TEST_CUE")} {
		if p == "" {
			continue
		}
		d, err := Inspect(p)
		if err != nil {
			t.Fatal(p, err)
		}
		if d.Startup != sample.Startup {
			t.Errorf("%s: startup %q", p, d.Startup)
		}
	}
}

func TestInspectRepair(t *testing.T) {
	good := sample.ISO()
	raw := make([]byte, 0, len(good)/2048*2352) // MODE2 form 1 sectors
	for i := 0; i < len(good); i += 2048 {
		sec := make([]byte, 2352)
		copy(sec, "\x00\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\x00")
		sec[15] = 2
		copy(sec[24:], good[i:i+2048])
		raw = append(raw, sec...)
	}
	badpt := sample.Path(t, "badpt.iso", sample.BrokenPathTable())
	for _, c := range []struct {
		f          string
		e111, e112 bool
	}{
		{sample.Path(t, "good.iso", good), false, false},
		{sample.Path(t, "badsys.iso", sample.WrongSystemID()), false, true},
		{badpt, true, false},
		{sample.Path(t, "raw.iso", raw), false, false},
	} {
		d, err := Inspect(c.f)
		if err != nil {
			t.Fatal(err)
		}
		if d.Err111 != c.e111 || d.Err112 != c.e112 || d.Startup != sample.Startup {
			t.Errorf("%s: %s", c.f, d)
		}
	}
	fixed := filepath.Join(t.TempDir(), "badpt.fixed.iso")
	if err := RepairPathTable(badpt, fixed, nil); err != nil {
		t.Fatal(err)
	}
	d, _ := Inspect(fixed)
	if d.Err111 || !d.PathTableOK {
		t.Errorf("repair failed: %s", d)
	}
}

func TestLayoutsAndHints(t *testing.T) {
	if _, ok := LayoutFor(2352, 2); !ok {
		t.Error("LayoutFor 2352/2")
	}
	if l, _ := LayoutFor(2048, 1); l.Raw() {
		t.Error("2048 is not raw")
	}
	for magic, want := range map[string]string{"PK\x03\x04": "ZIP", "7z\xbc\xaf\x27\x1c": "7-Zip", "CISO": "CSO", "MComprHD": "CHD", "[CloneCD]": "CloneCD", "MEDIA DESCRIPTOR": "MDS"} {
		if h := MagicHint(bytesReader(magic + "................................")); !strings.Contains(h, want) {
			t.Errorf("%q → %q", magic, h)
		}
	}
}

func TestLZ4Block(t *testing.T) {
	// hand-encoded: 3 literals "abc", match offset 3 length 10, last literals "xyz"
	src := []byte{0x36, 'a', 'b', 'c', 3, 0, 0x30, 'x', 'y', 'z'}
	out, err := lz4Block(src, 16)
	if err != nil || string(out) != "abcabcabcabcaxyz" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := lz4Block([]byte{0x10, 'a', 9, 0}, 8); err == nil {
		t.Error("invalid offset accepted")
	}
}

type bytesReader string

func (b bytesReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
