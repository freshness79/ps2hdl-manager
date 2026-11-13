package apa

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Compares Scan() with the real hdl_dump on REAL_DEV (APA disk) using REAL_SELF (embedded tools).
func TestScanMatchesHdlDump(t *testing.T) {
	dev, self := os.Getenv("REAL_DEV"), os.Getenv("REAL_SELF")
	if dev == "" || self == "" {
		t.Skip()
	}
	d, err := Scan(dev)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hdl(self, "hdl_toc", dev, "--csv")
	if err != nil {
		t.Fatal(string(out))
	}
	type row struct{ typ, size, flags, dma, startup, name string }
	var rows []row
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Split(l, ";")
		if len(f) >= 6 && (strings.TrimSpace(f[0]) == "CD" || strings.TrimSpace(f[0]) == "DVD") {
			rows = append(rows, row{strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[2]), strings.TrimSpace(f[3]), strings.TrimSpace(f[4]), strings.TrimSpace(strings.Join(f[5:], ";"))})
		}
	}
	if len(rows) != len(d.Games) {
		t.Fatalf("hdl_dump %d games, scan %d", len(rows), len(d.Games))
	}
	for i, r := range rows {
		g := d.Games[i]
		typ := map[bool]string{true: "DVD", false: "CD"}[g.IsDVD]
		flags := strings.TrimPrefix(g.Flags(), "+")
		if flags == "" {
			flags = "0"
		}
		got := row{typ, strconv.FormatUint(g.SizeKB, 10) + "KB", flags, g.DMAString(), g.Startup, g.Name}
		if got != r {
			t.Errorf("game %d:\n hdl_dump %+v\n scan     %+v", i, r, got)
		} else {
			t.Logf("OK %-5s %-12s %-6s %-4s %s [%s]", got.typ, got.startup, got.flags, got.dma, got.name, g.Partition)
		}
	}
	m := regexp.MustCompile(`total (\d+)MB, used (\d+)MB, available (\d+)MB`).FindStringSubmatch(string(out))
	if m == nil || m[1] != strconv.FormatUint(d.TotalMB, 10) || m[2] != strconv.FormatUint(d.UsedMB, 10) || m[3] != strconv.FormatUint(d.FreeMB, 10) {
		t.Errorf("usage: hdl_dump %v, scan %d/%d/%d", m, d.TotalMB, d.UsedMB, d.FreeMB)
	}
	// partition list vs "hdl_dump toc"
	toc, _ := hdl(self, "toc", dev)
	var ids []string
	re := regexp.MustCompile(`^0x[0-9a-f]{4}\s+\S+\s+\d+\s+\d+MB (.+?)\s*$`)
	for _, l := range strings.Split(string(toc), "\n") {
		if mm := re.FindStringSubmatch(strings.TrimSpace(l)); mm != nil {
			ids = append(ids, mm[1])
		}
	}
	var mains []string
	for _, p := range d.Partitions {
		if p.IsMain() {
			mains = append(mains, p.ID)
		}
	}
	if strings.Join(ids, "|") != strings.Join(mains, "|") {
		t.Errorf("partitions:\n toc  %v\n scan %v", ids, mains)
	}
	t.Logf("usage %d/%d/%d MB, %d partitions (%d main)", d.TotalMB, d.UsedMB, d.FreeMB, len(d.Partitions), len(mains))
}

// hdl runs the app's built-in hdl_dump (child mode is selected by the environment).
func hdl(self string, args ...string) ([]byte, error) {
	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), "PS2HDL_INTERNAL_TOOL=hdl_dump")
	return cmd.CombinedOutput()
}
