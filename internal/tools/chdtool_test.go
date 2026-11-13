package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/testenv/sample"
)

func TestCHD(t *testing.T) {
	c := &CHDTool{} // built-in only: no chdman involved
	ctx := context.Background()
	work := t.TempDir()
	dvd := filepath.Join(work, "dvd.chd")
	if err := c.CreateDVD(ctx, sample.Path(t, "s.iso", nil), dvd, nil); err != nil {
		t.Fatal(err)
	}
	inputs := map[string]string{"dvd": dvd, "cd-v4": sample.Path(t, "cd.chd", sample.CHDv4CD(sample.ISO()))}
	for f, src := range inputs {
		iso, err := c.ToISO(ctx, src, work, nil)
		if err != nil {
			t.Fatal(f, err)
		}
		d, err := disc.Inspect(iso)
		if err != nil || d.Startup != sample.Startup || d.Raw || d.Err111 {
			t.Fatalf("%s: %v %s", f, err, d)
		}
		st, _ := os.Stat(iso)
		t.Logf("%s → %s (%d bytes, %s)", f, iso, st.Size(), d.Format)
		// recompress to a 2048-byte DVD CHD and check the round trip (and with chdman, if present)
		out := work + "/" + f + ".dvd.chd"
		if err := c.CreateDVD(ctx, iso, out, nil); err != nil {
			t.Fatal(err)
		}
		if k, _ := c.Kind(ctx, out); k != "DVD" {
			t.Fatal("not a DVD CHD", k)
		}
		if p, err := exec.LookPath("chdman"); err == nil {
			if b, err := exec.Command(p, "verify", "-i", out).CombinedOutput(); err != nil {
				t.Fatalf("chdman verify: %s", b)
			}
		}
		os.Remove(iso)
		iso2, err := c.ToISO(ctx, out, work, nil)
		if err != nil {
			t.Fatal(err)
		}
		if d2, _ := disc.Inspect(iso2); d2.Startup != sample.Startup {
			t.Fatal("round trip")
		}
		os.Remove(iso2)
	}
}
