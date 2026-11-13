package chd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpeed(t *testing.T) {
	src := os.Getenv("CHD_BENCH") // a large CHD to time the extraction with
	if src == "" {
		t.Skip("CHD_BENCH not set")
	}
	out := filepath.Join(t.TempDir(), "out.iso")
	st := time.Now()
	if err := ExtractISO(src, out, nil); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(out)
	d := time.Since(st)
	t.Logf("%d MB in %v = %.1f MB/s", fi.Size()>>20, d, float64(fi.Size())/1e6/d.Seconds())
}
