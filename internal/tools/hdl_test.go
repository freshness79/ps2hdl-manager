package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHDLToc(t *testing.T) {
	out := `type startup                  size flags  dma    name
DVD  SLUS_900.01          2457600KB  +1+2    *u4   Sample Game Alpha
CD   SCES_900.07           524288KB          *u4   Sample Game Beta
DVD  SLES_900.05          4194304KB  +3     *m2   Sample Game Gamma - Long Subtitle
total 7176192 MB, used 7176 MB, available 0 MB`
	g := parseHDLToc(out)
	if len(g) != 3 {
		t.Fatalf("expected 3 games, found %d", len(g))
	}
	if g[0].Name != "Sample Game Alpha" || g[0].Flags != "+1+2" || g[0].DMA != "*u4" || g[0].Startup != "SLUS_900.01" {
		t.Errorf("riga 0 errata: %+v", g[0])
	}
	if g[1].Type != "CD" || g[1].Flags != "" || g[1].Name != "Sample Game Beta" {
		t.Errorf("riga 1 errata: %+v", g[1])
	}
	if g[2].Name != "Sample Game Gamma - Long Subtitle" || g[2].DMA != "*m2" {
		t.Errorf("riga 2 errata: %+v", g[2])
	}
}

func TestParseHDLToc092(t *testing.T) {
	plain := "type     size flags           dma startup      name\n" +
		"DVD  2457600KB             1+2 *u4 SLUS_900.01  Sample Game Alpha\n" +
		"CD    524288KB               0     SCES_900.07  Sample Game Beta\n"
	g := parseHDLToc(plain)
	if len(g) != 2 || g[0].Startup != "SLUS_900.01" || g[0].Flags != "+1+2" || g[0].DMA != "*u4" || g[0].Name != "Sample Game Alpha" || g[0].SizeKB != 2457600 {
		t.Fatalf("plain 0.9.2: %+v", g)
	}
	if g[1].Flags != "" || g[1].DMA != "" || g[1].Startup != "SCES_900.07" || g[1].Name != "Sample Game Beta" {
		t.Fatalf("plain 0.9.2 row 2: %+v", g[1])
	}
	csv := "type     size flags           dma startup      name\n" +
		"DVD;2457600KB;            1+2;*u4;SLUS_900.01 ;Sample Game Alpha\n" +
		"CD ; 524288KB;              0;   ;SCES_900.07 ;Sample Game Beta\n"
	c := parseHDLTocCSV(csv)
	if len(c) != 2 || c[0] != g[0] || c[1] != g[1] {
		t.Fatalf("csv: %+v", c)
	}
}

func TestProbe(t *testing.T) {
	out := "hdl_dump-0.9.2 by The W1zard 0f 0z\nUsage: x command arguments\n\nCommands list:\n  toc            display\n  hdl_toc        display a list\n  modify         modify \n"
	h := &HDL{}
	if m := versionRe.FindStringSubmatch(out); m == nil || m[1] != "0.9.2" {
		t.Fatal("version", m)
	}
	caps := map[string]bool{}
	for _, m := range cmdRe.FindAllStringSubmatch(out, -1) {
		caps[m[1]] = true
	}
	h.caps = caps
	if h.Supports("query") || !h.Supports("modify") || !h.Supports("hdl_toc") {
		t.Fatal(caps)
	}
}

func TestPercent(t *testing.T) {
	if p, ok := ParsePercent("  45%,  12 MB/sec,  00:05:10 remaining"); !ok || p != 0.45 {
		t.Error(p, ok)
	}
}

func TestRenameTwoStep(t *testing.T) {
	p := os.Getenv("FAKE_HDL")
	if p == "" {
		t.Skip()
	}
	h := &HDL{Path: p}
	if err := h.Rename(context.Background(), "hdd1:", "SAMPLE GAME ALPHA", "Sample Game Alpha", nil); err != nil {
		t.Fatal(err)
	}
}

func TestPartName(t *testing.T) {
	cases := map[[2]string]string{
		{"SLUS_900.01", "Test Game"}:                 "PP.SLUS-90001..TEST_GAME",
		{"SLUS_900.02", "Sample Game Epsilon Extra"}: "PP.SLUS-90002..SAMPLE_GAME_EPSI",
		{"SCES_900.07", "Other Game"}:                "PP.SCES-90007..OTHER_GAME",
		{"MAIN.ELF", "Homebrew"}:                     "PP.MAIN-00000..HOMEBREW",
	}
	for in, want := range cases {
		if p := HDLPartName(in[0], in[1], "PP."); p != want {
			t.Errorf("%v: got %q want %q", in, p, want)
		}
	}
	parts := []string{"__mbr", "PP.SLUS-90001..TEST_GAME", "PP.SCES-90007..OTHER_GAME", "PP.SLES-90004..SAMPLE GAME DELTA"}
	if m, _ := FindGamePartition(Game{Startup: "SCES_900.07", Name: "Renamed"}, parts); m != "PP.SCES-90007..OTHER_GAME" {
		t.Error(m)
	}
	if m, _ := FindGamePartition(Game{Startup: "SLES_900.04", Name: "Sample Game Delta"}, parts); m != "PP.SLES-90004..SAMPLE GAME DELTA" {
		t.Error(m)
	}
	if !tocPartRe.MatchString("0x1337 01020000.: 12  4096MB PP.SLES-90004..SAMPLE GAME DELTA") {
		t.Error("toc regex")
	}
}

func TestDiskErrors(t *testing.T) {
	h := &HDL{Path: "/nonexistent-tool"}
	_, _, err := h.List(context.Background(), "/dev/does-not-exist", nil)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing disk: %v", err)
	}
	f := filepath.Join(t.TempDir(), "blank.img")
	if err := os.WriteFile(f, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = h.List(context.Background(), f, nil)
	if err == nil || !strings.Contains(err.Error(), "not a PlayStation 2") {
		t.Errorf("blank disk: %v", err)
	}
}
