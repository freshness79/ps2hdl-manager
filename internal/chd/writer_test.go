package chd

import (
	"bytes"
	"github.com/freshness79/ps2hdl-manager/internal/testenv/sample"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateDVDWithChdman(t *testing.T) {
	chdman, err := exec.LookPath("chdman")
	if err != nil {
		t.Skip()
	}
	isos := []string{sample.Path(t, "sample.iso", nil)}
	if testDir != "" {
		isos = append(isos, filepath.Join(testDir, "mix.iso"), filepath.Join(testDir, "real.iso"))
	}
	for _, iso := range isos {
		want, err := os.ReadFile(iso)
		if err != nil {
			continue
		}
		for _, compat := range []bool{false, true} {
			out := filepath.Join(t.TempDir(), "ours.chd")
			st := time.Now()
			if err := CreateDVD(iso, out, WriteOptions{UseZstd: compat}, nil); err != nil {
				t.Fatal(err)
			}
			el := time.Since(st)
			fi, _ := os.Stat(out)
			// 1) MAME's own verifier
			if b, err := exec.Command(chdman, "verify", "-i", out).CombinedOutput(); err != nil {
				t.Fatalf("%s compat=%v: chdman verify failed:\n%s", filepath.Base(iso), compat, b)
			}
			// 2) MAME extraction must give back the identical ISO
			ext := out + ".iso"
			if b, err := exec.Command(chdman, "extractdvd", "-i", out, "-o", ext, "-f").CombinedOutput(); err != nil {
				t.Fatalf("chdman extractdvd: %s", b)
			}
			got, _ := os.ReadFile(ext)
			if !bytes.Equal(got, want) {
				t.Fatalf("%s: chdman extraction differs", filepath.Base(iso))
			}
			// 3) our reader
			if g := readISO(t, out); !bytes.Equal(g, want) {
				t.Fatalf("%s: our extraction differs", filepath.Base(iso))
			}
			t.Logf("%-9s zstd=%-5v %6.1f MB → %6.1f MB in %v (%.1f MB/s) — chdman verify OK",
				filepath.Base(iso), compat, float64(len(want))/1e6, float64(fi.Size())/1e6, el.Round(time.Millisecond), float64(len(want))/1e6/el.Seconds())
		}
	}
}
