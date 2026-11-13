package chd

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// testDir holds optional reference images made with MAME's chdman (CHD_TESTDATA,
// see docs/DEVELOPMENT.md): mix.iso and one CHD per codec. Tests that need it
// are skipped when it is not set.
var testDir = os.Getenv("CHD_TESTDATA")

func readISO(t *testing.T, path string) []byte {
	c, err := Open(path)
	if err != nil {
		t.Fatal(path, err)
	}
	defer c.Close()
	if err := c.Verify(nil); err != nil {
		t.Fatalf("%s: verify: %v", path, err)
	}
	var buf bytes.Buffer
	if err := c.WriteISO(&buf, nil); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return buf.Bytes()
}

func TestReadAllCodecs(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(testDir, "mix.iso"))
	if testDir == "" || err != nil {
		t.Skip("CHD_TESTDATA not set")
	}
	files, _ := filepath.Glob(filepath.Join(testDir, "*.chd"))
	for _, f := range files {
		if filepath.Base(f) == "raw_mode1.chd" || filepath.Base(f) == "ours.chd" {
			continue
		}
		got := readISO(t, f)
		if !bytes.Equal(got, want) {
			t.Errorf("%s: ISO differs (%d vs %d bytes)", filepath.Base(f), len(got), len(want))
		} else {
			t.Logf("%s OK", filepath.Base(f))
		}
	}
}

// edc computes the CD-ROM EDC (CRC32, poly 0xD8018001 reflected).
func edc(b []byte) uint32 {
	var crc uint32
	for _, x := range b {
		crc ^= uint32(x)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xd8018001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func TestRawCDWithECC(t *testing.T) {
	chdman, err := exec.LookPath("chdman")
	if err != nil {
		t.Skip()
	}
	iso, err := os.ReadFile(filepath.Join(testDir, "mix.iso"))
	if testDir == "" || err != nil {
		t.Skip("CHD_TESTDATA not set")
	}
	// build raw MODE1/2352 sectors with sync, header, EDC and ECC
	n := len(iso) / 2048
	raw := make([]byte, n*2352)
	for i := 0; i < n; i++ {
		s := raw[i*2352 : (i+1)*2352]
		copy(s, cdSyncHeader[:])
		lba := i + 150
		bcd := func(v int) byte { return byte(v/10<<4 | v%10) }
		s[12], s[13], s[14], s[15] = bcd(lba/75/60), bcd(lba/75%60), bcd(lba%75), 1
		copy(s[16:], iso[i*2048:(i+1)*2048])
		binary.LittleEndian.PutUint32(s[2064:], edc(s[:2064]))
		eccGenerate(s)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "raw.bin"), raw, 0o644)
	os.WriteFile(filepath.Join(dir, "raw.cue"), []byte("FILE \"raw.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\n"), 0o644)
	out := filepath.Join(dir, "raw.chd")
	if b, err := exec.Command(chdman, "createcd", "-i", filepath.Join(dir, "raw.cue"), "-o", out, "-f").CombinedOutput(); err != nil {
		t.Fatal(string(b))
	}
	eccRegenerated.Store(0)
	got := readISO(t, out) // Verify() checks the raw SHA-1 including regenerated ECC
	if eccRegenerated.Load() == 0 {
		t.Fatal("ECC regeneration path not exercised")
	}
	t.Logf("ECC regenerated for %d sectors; SHA-1 verified", eccRegenerated.Load())
	if !bytes.Equal(got, iso) {
		t.Fatal("ISO differs")
	}
}
