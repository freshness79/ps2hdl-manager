package builtin_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	chdlib "github.com/freshness79/ps2hdl-manager/internal/chd"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
	"github.com/freshness79/ps2hdl-manager/internal/testenv"
	"github.com/freshness79/ps2hdl-manager/internal/testenv/sample"
)

// probePrepare runs the plug-in chain like the installer does, including the
// installer's final check that only 2048-byte sectors reach the PS2 disk.
func probePrepare(t *testing.T, path, wantFormat string) (*formats.Info, *formats.Prepared) {
	t.Helper()
	path = formats.Primary(path)
	f, err := formats.For(path)
	if err != nil {
		t.Fatalf("%s: %v", filepath.Base(path), err)
	}
	if f.Name() != wantFormat {
		t.Fatalf("%s: format %q, want %q", filepath.Base(path), f.Name(), wantFormat)
	}
	info, err := f.Probe(context.Background(), path)
	if err != nil {
		t.Fatalf("%s probe: %v", filepath.Base(path), err)
	}
	if info.Unsupported != "" || info.Problem != "" {
		return info, nil
	}
	prep, err := f.Prepare(context.Background(), path, info, formats.Env{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("%s prepare: %v", filepath.Base(path), err)
	}
	if !formats.IsNative(f) {
		if err := disc.Verify2048(prep.Path); err != nil {
			t.Fatalf("%s: output not 2048-byte sectors: %v", filepath.Base(path), err)
		}
	}
	return info, prep
}

// sameData checks the prepared output carries exactly the user data of iso.
func sameData(t *testing.T, prep *formats.Prepared, iso []byte) {
	t.Helper()
	got, err := os.ReadFile(prep.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, iso) {
		t.Errorf("prepared data differs from the source ISO (%d vs %d bytes)", len(got), len(iso))
	}
}

const testID = "SLUS_900.10"

// realisticISO builds a PS2-like ISO with genisoimage (an independent ISO9660 /
// UDF writer): SYSTEM.CNF + executable + a few MB of mixed data.
func realisticISO(t *testing.T, udf bool) string {
	t.Helper()
	if _, err := exec.LookPath("genisoimage"); err != nil {
		t.Skip("genisoimage not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "DATA"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "SYSTEM.CNF"), []byte("BOOT2 = cdrom0:\\"+testID+";1\r\nVER = 1.00\r\nVMODE = NTSC\r\n"), 0o644)
	rng := rand.New(rand.NewSource(7))
	exe := make([]byte, 600<<10)
	rng.Read(exe[:200<<10]) // incompressible part
	for i := 200 << 10; i < len(exe); i++ {
		exe[i] = "PLAYSTATION2 GAME DATA "[i%23]
	}
	_ = os.WriteFile(filepath.Join(src, testID), exe, 0o644)
	big := make([]byte, 3<<20)
	for i := range big {
		big[i] = byte(i / 4096 % 251)
	}
	_ = os.WriteFile(filepath.Join(src, "DATA", "LEVEL.BIN"), big, 0o644)
	out := filepath.Join(dir, "game.iso")
	args := []string{"-quiet", "-sysid", "PLAYSTATION", "-V", "TESTGAME", "-o", out}
	if udf {
		args = append(args, "-udf")
	}
	if b, err := exec.Command("genisoimage", append(args, src)...).CombinedOutput(); err != nil {
		t.Fatalf("genisoimage: %v %s", err, b)
	}
	return out
}

func TestFormatPlugins(t *testing.T) {
	iso := testenv.LoadISO(t)
	ref, err := disc.Inspect(testenv.ISOPath(t))
	if err != nil || ref.Startup == "" {
		t.Fatalf("reference image: %v %+v", err, ref)
	}
	want := ref.Startup

	t.Run("ISO passthrough", func(t *testing.T) {
		p := testenv.Write(t, "game.iso", iso)
		info, prep := probePrepare(t, p, "ISO")
		if info.Startup != want || prep.Path != p || prep.Cleanup != nil {
			t.Errorf("a plain ISO must be used as is: info %+v prep %+v", info, prep)
		}
	})
	layouts := []struct {
		name      string
		size, off int
	}{
		{"MODE1-2352", 2352, 16}, {"MODE2-2352", 2352, 24}, {"MODE2-2336", 2336, 8},
		{"MODE1-2448sub", 2448, 16}, {"MODE2-2448sub", 2448, 24},
	}
	for _, l := range layouts {
		t.Run("BIN "+l.name, func(t *testing.T) {
			info, prep := probePrepare(t, testenv.Write(t, "game.bin", testenv.ToLayout(iso, l.size, l.off)), "BIN")
			if info.Startup != want || info.Media != formats.CD {
				t.Errorf("info %+v", info)
			}
			sameData(t, prep, iso)
			prep.Close()
			if _, err := os.Stat(prep.Path); err == nil {
				t.Error("temporary ISO not removed")
			}
		})
		t.Run("IMG "+l.name, func(t *testing.T) {
			_, prep := probePrepare(t, testenv.Write(t, "game.img", testenv.ToLayout(iso, l.size, l.off)), "IMG")
			sameData(t, prep, iso)
		})
		t.Run("misnamed ISO "+l.name, func(t *testing.T) {
			_, prep := probePrepare(t, testenv.Write(t, "raw.iso", testenv.ToLayout(iso, l.size, l.off)), "ISO")
			sameData(t, prep, iso)
		})
	}
	t.Run("BIN/CUE pregap + audio", func(t *testing.T) {
		raw := testenv.ToRaw(iso, 2)
		pregap := make([]byte, 150*2352)              // 2-second pregap before INDEX 01
		audio := bytes.Repeat([]byte{0x11}, 300*2352) // audio track after the data
		bin := testenv.Write(t, "Game (Track 1).bin", append(append(pregap, raw...), audio...))
		dir := filepath.Dir(bin)
		testenv.CueSheet(t, dir, "Game.cue", "FILE \"Game (Track 1).bin\" BINARY\n  TRACK 01 MODE2/2352\n    INDEX 00 00:00:00\n    INDEX 01 00:02:00\n"+
			"  TRACK 02 AUDIO\n    PREGAP 00:02:00\n    INDEX 01 "+msf(150+len(raw)/2352)+"\n")
		info, prep := probePrepare(t, bin, "BIN/CUE") // the .bin is grouped with its CUE
		if info.Startup != want || !strings.Contains(strings.Join(info.Notes, " "), "1 audio track") {
			t.Errorf("info %+v", info)
		}
		sameData(t, prep, iso)
	})
	t.Run("BIN/CUE stored pregap", func(t *testing.T) { // next track's INDEX 00 ends the data track
		raw := testenv.ToRaw(iso, 1)
		n := len(raw) / 2352
		bin := testenv.Write(t, "g.bin", append(append(raw, make([]byte, 150*2352)...), bytes.Repeat([]byte{7}, 100*2352)...))
		testenv.CueSheet(t, filepath.Dir(bin), "g.cue", "FILE \"g.bin\" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n  TRACK 02 AUDIO\n    INDEX 00 "+msf(n)+"\n    INDEX 01 "+msf(n+150)+"\n")
		_, prep := probePrepare(t, bin, "BIN/CUE")
		sameData(t, prep, iso)
	})
	t.Run("BIN/CUE multi-file", func(t *testing.T) {
		dir := filepath.Dir(testenv.Write(t, "t1.bin", testenv.ToRaw(iso, 1)))
		_ = os.WriteFile(filepath.Join(dir, "t2.bin"), make([]byte, 100*2352), 0o644)
		cue := testenv.CueSheet(t, dir, "multi.cue", "FILE \"t1.bin\" BINARY\r\n  TRACK 01 MODE1/2352\r\n    INDEX 01 00:00:00\r\nFILE \"t2.bin\" BINARY\r\n  TRACK 02 AUDIO\r\n    INDEX 01 00:00:00\r\n")
		_, prep := probePrepare(t, cue, "BIN/CUE")
		sameData(t, prep, iso)
	})
	t.Run("CUE missing BIN", func(t *testing.T) {
		cue := testenv.CueSheet(t, t.TempDir(), "x.cue", "FILE \"gone.bin\" BINARY\n TRACK 01 MODE2/2352\n INDEX 01 00:00:00\n")
		_, err := formats.For(cue)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := formats.For(cue)
		if _, err := f.Probe(context.Background(), cue); err == nil || !strings.Contains(err.Error(), "gone.bin") {
			t.Errorf("missing data file not explained: %v", err)
		}
	})
	for _, c := range []struct {
		name string
		mode byte
		size int
		off  int
		dvd  bool
	}{{"MODE2-2352", 0xAB, 2352, 24, false}, {"MODE1-2448sub", 0xAA, 2448, 16, false}, {"DVD-2048", 0xAA, 2048, 0, true}} {
		t.Run("MDS/MDF "+c.name, func(t *testing.T) {
			data := testenv.ToLayout(iso, c.size, c.off)
			if c.size == 2048 {
				data = iso
			}
			mdf := testenv.Write(t, "Game.mdf", append(data, make([]byte, 75*2352)...)) // + an audio track
			_ = os.WriteFile(strings.TrimSuffix(mdf, ".mdf")+".mds", testenv.MDS(c.mode, c.size, 0, len(iso)/2048, 1, c.dvd), 0o644)
			info, prep := probePrepare(t, mdf, "MDF/MDS (Alcohol)") // the .mdf is grouped with its .mds
			if info.Startup != want {
				t.Errorf("info %+v", info)
			}
			sameData(t, prep, iso)
		})
	}
	t.Run("MDF without MDS", func(t *testing.T) {
		_, prep := probePrepare(t, testenv.Write(t, "Lone.mdf", testenv.ToRaw(iso, 2)), "MDF/MDS (Alcohol)")
		sameData(t, prep, iso)
	})
	for _, mode := range []int{1, 2} {
		t.Run("CCD/IMG/SUB MODE"+string(rune('0'+mode)), func(t *testing.T) {
			raw := testenv.ToRaw(iso, mode)
			img := testenv.Write(t, "Game.img", append(raw, make([]byte, 200*2352)...))
			base := strings.TrimSuffix(img, ".img")
			_ = os.WriteFile(base+".ccd", []byte(testenv.CCD(mode, len(raw)/2352+150)), 0o644)
			_ = os.WriteFile(base+".sub", make([]byte, (len(raw)/2352+200)*96), 0o644)
			for _, entry := range []string{base + ".img", base + ".sub", base + ".ccd"} { // any file of the set
				if formats.Primary(entry) != base+".ccd" {
					t.Errorf("%s not grouped with the .ccd", filepath.Base(entry))
				}
			}
			info, prep := probePrepare(t, base+".sub", "CCD/IMG/SUB (CloneCD)")
			if info.Startup != want || !strings.Contains(strings.Join(info.Notes, " "), "audio") {
				t.Errorf("info %+v", info)
			}
			sameData(t, prep, iso)
		})
	}
	nrgCases := []struct {
		name string
		img  []byte
	}{
		{"v2-DAO-2048", testenv.NRGv2DAO(iso, 2048)},
		{"v2-DAO-raw2352", testenv.NRGv2DAO(testenv.ToRaw(iso, 2), 2352)},
		{"v1-TAO-2048", testenv.NRGv1TAO(iso)},
	}
	for _, c := range nrgCases {
		t.Run("NRG "+c.name, func(t *testing.T) {
			info, prep := probePrepare(t, testenv.Write(t, "game.nrg", c.img), "NRG (Nero)")
			if info.Startup != want || prep.Startup != want {
				t.Errorf("startup %q/%q, want %q", info.Startup, prep.Startup, want)
			}
			sameData(t, prep, iso)
		})
	}
	t.Run("NRG disguised as ISO", func(t *testing.T) {
		probePrepare(t, testenv.Write(t, "game.iso", testenv.NRGv2DAO(iso, 2048)), "NRG (Nero)")
	})
	t.Run("PKG", func(t *testing.T) {
		info, prep := probePrepare(t, testenv.Write(t, "store.iso", append([]byte("\x7fPKG"), make([]byte, 1000)...)), "PKG")
		if prep != nil || !strings.Contains(info.Unsupported, "PS3 PKG") {
			t.Errorf("info %+v", info)
		}
	})
	t.Run("CHD", func(t *testing.T) {
		chd := filepath.Join(t.TempDir(), "game.chd")
		if err := chdlib.CreateDVD(testenv.ISOPath(t), chd, chdlib.WriteOptions{}, nil); err != nil {
			t.Fatal(err)
		}
		info, prep := probePrepare(t, chd, "CHD")
		if !info.IDAtPrepare || info.Media != formats.DVD || prep.Startup != want {
			t.Errorf("info %+v prep %+v", info, prep)
		}
		sameData(t, prep, iso)
	})
	t.Run("CHD v4 (CD)", func(t *testing.T) {
		info, prep := probePrepare(t, testenv.Write(t, "old.chd", testenv.CHDv4CD(iso)), "CHD")
		if info.Media != formats.CD || prep.Startup != want {
			t.Errorf("info %+v prep %+v", info, prep)
		}
		sameData(t, prep, iso)
	})
	t.Run("ISO error 111 fix", func(t *testing.T) {
		p := testenv.Write(t, "broken.iso", sample.BrokenPathTable())
		info, _ := probePrepare(t, p, "ISO")
		if info.Problem == "" || info.Fix == nil {
			t.Fatalf("no problem/fix: %+v", info)
		}
		dst, err := info.Fix.Run(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if info2, prep := probePrepare(t, dst, "ISO"); info2.Problem != "" || prep == nil {
			t.Errorf("repaired copy still broken: %+v", info2)
		}
	})
}

// Images produced by independent tools: genisoimage (ISO9660 + UDF bridge) and a
// Python CSO/ZSO writer using zlib and python-lz4.
func TestFormatPluginsReference(t *testing.T) {
	src := realisticISO(t, true)
	iso, _ := os.ReadFile(src)

	t.Run("UDF bridge (.udf)", func(t *testing.T) {
		p := testenv.Write(t, "game.udf", iso)
		info, prep := probePrepare(t, p, "UDF")
		if info.Startup != testID || prep.Path != p {
			t.Errorf("info %+v prep %+v", info, prep)
		}
	})
	t.Run("UDF only", func(t *testing.T) {
		only := append([]byte(nil), iso...)
		clear(only[16*2048 : 18*2048]) // drop the ISO9660 descriptors, keep UDF
		f, _ := formats.For(testenv.Write(t, "udfonly.udf", only))
		info, err := f.Probe(context.Background(), testenv.Write(t, "udfonly2.udf", only))
		if err != nil {
			t.Fatal(err)
		}
		if info.Problem == "" || info.Startup != testID {
			t.Errorf("UDF-only image: startup %q (read through UDF), problem %q", info.Startup, info.Problem)
		}
		if !strings.Contains(info.Details, "UDF") {
			t.Error("details do not explain the UDF problem")
		}
	})
	py := exec.Command("python3", "-c", "import lz4.block")
	if py.Run() != nil {
		t.Skip("python lz4 module not installed")
	}
	dir := t.TempDir()
	if b, err := exec.Command("python3", "../../../scripts/make_test_images.py", src, dir).CombinedOutput(); err != nil {
		t.Fatalf("make_test_images.py: %v %s", err, b)
	}
	for _, c := range []struct{ file, format string }{
		{"v1.cso", "CSO/ZSO"}, {"v1-align2.cso", "CSO/ZSO"}, {"v2.cso", "CSO/ZSO"}, {"game.zso", "CSO/ZSO"},
	} {
		t.Run(c.file, func(t *testing.T) {
			info, prep := probePrepare(t, filepath.Join(dir, c.file), c.format)
			if info.Startup != testID {
				t.Errorf("startup %q", info.Startup)
			}
			sameData(t, prep, iso)
		})
	}
	t.Run("CSO renamed to .iso", func(t *testing.T) {
		b, _ := os.ReadFile(filepath.Join(dir, "v1.cso"))
		probePrepare(t, testenv.Write(t, "compressed.iso", b), "CSO/ZSO")
	})
}

// Problems must come with an explanation, not just "unsupported".
func TestFormatErrorsExplained(t *testing.T) {
	cases := []struct{ name, file, want string }{
		{"zip as iso", "game.iso", "ZIP archive"},
		{"rar as bin", "game.bin", "RAR archive"},
		{"zeros", "game.img", "empty (zeros)"},
		{"ecm", "game.bin", "ECM"},
	}
	contents := map[string][]byte{
		"zip as iso": append([]byte("PK\x03\x04"), make([]byte, 70000)...),
		"rar as bin": append([]byte("Rar!\x1a\x07\x00"), make([]byte, 70000)...),
		"zeros":      make([]byte, 70000),
		"ecm":        append([]byte("ECM\x00"), make([]byte, 70000)...),
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := testenv.Write(t, c.file, contents[c.name])
			f, err := formats.For(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Probe(context.Background(), p)
			var le *disc.LayoutError
			if err == nil || !strings.Contains(err.Error(), c.want) || !errors.As(err, &le) || !strings.Contains(le.Detail(), "2048-byte sectors") {
				t.Errorf("error not explanatory: %v", err)
			}
		})
	}
	t.Run("truncated", func(t *testing.T) {
		iso := testenv.LoadISO(t)
		raw := testenv.ToRaw(iso, 2)
		p := testenv.Write(t, "cut.bin", raw[:len(raw)-1000])
		f, _ := formats.For(p)
		info, err := f.Probe(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Prepare(context.Background(), p, info, formats.Env{WorkDir: t.TempDir()}); err == nil {
			// the incomplete last sector is dropped: still a valid result
			return
		}
	})
}

func msf(sectors int) string {
	m, s, f := sectors/(60*75), sectors/75%60, sectors%75
	return two(m) + ":" + two(s) + ":" + two(f)
}

func two(n int) string { return string([]byte{byte('0' + n/10), byte('0' + n%10)}) }

// TestFormatPluginsInstall injects the output of each plug-in with the real
// (built-in) hdl_dump: REAL_DEV / REAL_SELF as for the other integration tests.
func TestFormatPluginsInstall(t *testing.T) {
	h, p, dev := testenv.Tools(t)
	iso := testenv.LoadISO(t)
	ctx := context.Background()
	dir := t.TempDir()
	chd := filepath.Join(dir, "g.chd")
	if err := chdlib.CreateDVD(testenv.ISOPath(t), chd, chdlib.WriteOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	mdf := filepath.Join(dir, "m.mdf")
	_ = os.WriteFile(mdf, testenv.ToLayout(iso, 2448, 24), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "m.mds"), testenv.MDS(0xAB, 2448, 0, len(iso)/2048, 0, false), 0o644)
	img := filepath.Join(dir, "c.img")
	_ = os.WriteFile(img, testenv.ToRaw(iso, 2), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "c.ccd"), []byte(testenv.CCD(2)), 0o644)
	cases := []struct{ file, format, id string }{
		{testenv.Write(t, "a.bin", testenv.ToRaw(iso, 2)), "BIN", "SLUS_991.01"},
		{testenv.Write(t, "b.nrg", testenv.NRGv2DAO(testenv.ToRaw(iso, 2), 2352)), "NRG (Nero)", "SLUS_991.02"},
		{testenv.Write(t, "c.nrg", testenv.NRGv1TAO(iso)), "NRG (Nero)", "SLUS_991.03"},
		{chd, "CHD", "SLUS_991.04"},
		{mdf, "MDF/MDS (Alcohol)", "SLUS_991.05"},
		{img, "CCD/IMG/SUB (CloneCD)", "SLUS_991.06"},
		{testenv.Write(t, "d.bin", testenv.ToLayout(iso, 2336, 8)), "BIN", "SLUS_991.07"},
		{testenv.Write(t, "old.chd", testenv.CHDv4CD(iso)), "CHD", "SLUS_991.08"},
	}
	for i, c := range cases {
		info, prep := probePrepare(t, c.file, c.format)
		dvd := info.Media == formats.DVD || prep.Media == formats.DVD
		name := "Plugin Test " + string(rune('A'+i))
		err := h.Inject(ctx, dev, name, prep.Path, c.id, "", "*u4", dvd, nil)
		prep.Close()
		if err != nil {
			t.Errorf("%s: inject: %v", c.format, err)
		}
	}
	games, _, err := h.List(ctx, dev, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, g := range games {
		if strings.HasPrefix(g.Startup, "SLUS_991.0") {
			found++
			t.Logf("installed %-14s %s %s %.1f KB", g.Name, g.Type, g.Startup, float64(g.SizeKB))
			// read the game back from the PS2 disk: exactly the 2048-byte user data
			out := filepath.Join(t.TempDir(), "back.iso")
			if err := h.Extract(ctx, dev, g.Name, out, nil); err != nil {
				t.Errorf("%s: extract: %v", g.Name, err)
			} else if back, _ := os.ReadFile(out); !bytes.Equal(back, iso) {
				t.Errorf("%s: data on the PS2 disk differs from the source ISO (%d vs %d bytes)", g.Name, len(back), len(iso))
			}
			_ = p.RmPart(ctx, dev, g.Partition, nil)
		}
	}
	if found != len(cases) {
		t.Errorf("installed %d of %d", found, len(cases))
	}
}
