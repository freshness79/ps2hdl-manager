package tools_test

import (
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freshness79/ps2hdl-manager/internal/apa"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/testenv"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
)

// coverServer serves a JPEG front cover for any game (the default cover
// source is not used, so the tests stay offline and need no real game ID).
func coverServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = jpeg.Encode(w, image.NewRGBA(image.Rect(0, 0, 280, 400)), nil)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// compatServer is a stand-in for the info sheet and compatibility databases:
// the sample PAL game SCES_900.03 needs modes 2+3+6 (mask 38).
func compatServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info/SCES_900.03.cfg":
			_, _ = w.Write([]byte("Genre=Platformer\nPlayers=players/1\n"))
		case "/info/SLES_900.04.cfg":
			_, _ = w.Write([]byte("Genre=Action\nPlayers=players/2\n"))
		case "/list.txt":
			_, _ = w.Write([]byte("SCES_900.03\n"))
		case "/modes/SCES_900.03.cfg":
			_, _ = w.Write([]byte("$Compatibility=38\nModes=2+3+6\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Requires REAL_DEV (an APA disk) and REAL_SELF (the built app) — skipped otherwise.
func TestIntegration(t *testing.T) {
	h, p, dev := testenv.Tools(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	h.Probe(ctx)

	if err := h.Inject(ctx, dev, "Sample Game Alpha", testenv.ISOPath(t), "SLUS_900.06", "", "*u4", true, nil); err != nil {
		t.Fatal(err)
	}
	// direct case-only rename fails in hdl_dump; two-step must work
	if _, err := h.Run(ctx, nil, "modify", dev, "Sample Game Alpha", "SAMPLE GAME ALPHA"); err == nil {
		t.Log("note: direct case-only rename did not fail")
	} else {
		t.Log("direct rename fails as expected:", err)
	}
	if err := h.Rename(ctx, dev, "Sample Game Alpha", "SAMPLE GAME ALPHA", nil); err != nil {
		t.Fatal("two-step rename:", err)
	}
	games, usage, err := h.List(ctx, dev, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("games=%+v usage=%+v", games, usage)

	if err := p.EnsureOPL(ctx, dev, "+OPL", func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	cov, err := gamedb.DownloadCover(coverServer(t)+"/{ID}.jpg", "SLUS_900.06")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.PutArt(ctx, dev, "+OPL", filepath.Dir(cov), []string{filepath.Base(cov)}, nil); err != nil {
		t.Fatal(err)
	}
	// overwrite must work too
	if err := p.PutArt(ctx, dev, "+OPL", filepath.Dir(cov), []string{filepath.Base(cov)}, nil); err != nil {
		t.Fatal(err)
	}
	files, err := p.ListArt(ctx, dev, "+OPL")
	found := false
	for _, f := range files {
		found = found || f == "SLUS_900.06_COV.png"
	}
	if err != nil || !found {
		t.Fatal(files, err)
	}
	parts, err := h.HDLPartitions(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	var g tools.Game
	for _, x := range games {
		if x.Startup == "SLUS_900.06" {
			g = x
		}
	}
	part, _ := tools.FindGamePartition(g, parts)
	if part == "" {
		t.Fatal("partition not found", parts)
	}
	if err := p.RmPart(ctx, dev, part, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.RmArt(ctx, dev, "+OPL", []string{"SLUS_900.06_COV.jpg", "SLUS_900.06_COV.png"}, nil); err != nil {
		t.Fatal(err)
	}
	games2, _, _ := h.List(ctx, dev, nil)
	files2, _ := p.ListArt(ctx, dev, "+OPL")
	t.Logf("after delete: games=%+v art=%v", games2, files2)
	for _, x := range games2 {
		if x.Startup == "SLUS_900.06" {
			t.Fatal("not deleted")
		}
	}
	for _, f := range files2 {
		if f == "SLUS_900.06_COV.jpg" {
			t.Fatal("art not removed", files2)
		}
	}
}

func TestIntegrationCompat(t *testing.T) {
	h, p, dev := testenv.Tools(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	h.Probe(ctx)
	db := gamedb.NewTitleDB()
	st := gamedb.NewInfoStore(db)
	src := compatServer(t)
	st.SetSources(src+"/info/{STARTUP}.cfg", src+"/list.txt", src+"/modes/{STARTUP}.cfg")

	// the sample PAL game needs modes 2+3+6 per the (local) compat DB
	gi, _ := st.Get("SCES_900.03")
	t.Logf("compat info: %+v", *gi)
	if gi.ModesMask != 38 || gi.Players == 0 {
		t.Fatalf("unexpected info %+v", gi)
	}
	gi2, _ := st.Get("SLES_900.04")
	t.Logf("second game: genre=%q players=%d multi=%s compatKnown=%v", gi2.Genre, gi2.Players, gi2.MultiplayerText(), gi2.CompatKnown)

	if err := h.Inject(ctx, dev, "Sample Game Zeta - Long Subtitle", testenv.ISOPath(t), "SCES_900.03", "", "*u4", true, nil); err != nil {
		t.Fatal(err)
	}
	g := tools.Game{Startup: "SCES_900.03", Name: "Sample Game Zeta - Long Subtitle"}
	if _, err := h.Run(ctx, nil, "modify", dev, g.Name, gamedb.FlagsString(gi.ModesMask)); err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureOPL(ctx, dev, "+OPL", nil); err != nil {
		t.Fatal(err)
	}
	ex, err := p.GetCFGs(ctx, dev, "+OPL", []string{"SCES_900.03.cfg"})
	if err != nil {
		t.Fatal(err)
	}
	kv := gamedb.MergeGameCFG(gamedb.ParseCFG(ex["SCES_900.03.cfg"]), g.Name, gi, true)
	if err := p.PutCFGs(ctx, dev, "+OPL", map[string]string{"SCES_900.03.cfg": gamedb.FormatCFG(kv)}, nil); err != nil {
		t.Fatal(err)
	}
	back, err := p.GetCFG(ctx, dev, "+OPL", "SCES_900.03")
	if err != nil || !strings.Contains(back, "$Compatibility=38") {
		t.Fatalf("cfg readback: %q %v", back, err)
	}
	t.Logf("CFG on disk:\n%s", back)
	games, _, _ := h.List(ctx, dev, nil)
	for _, x := range games {
		if x.Startup == "SCES_900.03" {
			t.Logf("flags on disk: %q mask=%d", x.Flags, gamedb.FlagsToMask(x.Flags))
			if gamedb.FlagsToMask(x.Flags) != 38 {
				t.Fatal("flags not applied")
			}
			g = x
		}
	}
	parts, _ := h.HDLPartitions(ctx, dev)
	part, _ := tools.FindGamePartition(g, parts)
	t.Logf("partitions=%v → %q", parts, part)
	if part == "" {
		t.Fatal("no partition")
	}
	if err := p.RmPart(ctx, dev, part, nil); err != nil {
		t.Fatal(err)
	}
	// partition name with spaces (created by other tools) must be removable
	if _, err := p.Run(ctx, dev, "", []string{`mkpart "PP.SLES-90004..SAMPLE GAME DELTA" 128M PFS`}, nil); err != nil {
		t.Fatal(err)
	}
	ps, _ := p.Partitions(ctx, dev)
	t.Logf("pfs partitions: %v", ps)
	found := false
	for _, x := range ps {
		found = found || x == "PP.SLES-90004..SAMPLE GAME DELTA"
	}
	if !found {
		t.Fatal("partition with spaces not listed", ps)
	}
	if err := p.RmPart(ctx, dev, "PP.SLES-90004..SAMPLE GAME DELTA", nil); err != nil {
		t.Fatal(err)
	}
	ps, _ = p.Partitions(ctx, dev)
	for _, x := range ps {
		if strings.Contains(x, "DELTA") {
			t.Fatal("not removed", ps)
		}
	}
}

// REAL_DEV=/dev/loopN REAL_SELF=./ps2hdl-manager go test -run TestBatch
// Works on its own games (IDs SLUS_990.0x) and removes them at the end.
func TestBatch(t *testing.T) {
	h, p, dev := testenv.Tools(t)
	ctx := context.Background()
	ids := []string{"SLUS_990.01", "SLUS_990.02", "SLUS_990.03"}
	mine := func() ([]tools.Game, int) {
		d, err := apa.Scan(dev)
		if err != nil {
			t.Fatal(err)
		}
		all, _, err := h.List(ctx, dev, nil) // read through the same Go scanner
		if err != nil {
			t.Fatal(err)
		}
		var out []tools.Game
		for _, g := range all {
			for _, id := range ids {
				if g.Startup == id {
					out = append(out, g)
				}
			}
		}
		return out, len(d.Games)
	}
	for i, id := range ids {
		if err := h.Inject(ctx, dev, "Batch Seed "+string(rune('A'+i)), testenv.ISOPath(t), id, "", "*u4", true, nil); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		games, _ := mine()
		for _, g := range games {
			if err := p.RmPart(ctx, dev, g.Partition, nil); err != nil {
				t.Errorf("cleanup %s: %v", g.Partition, err)
			}
		}
	}()
	games, total := mine()
	if len(games) != len(ids) || !h.CanBatch(games...) {
		t.Fatalf("seed games: %+v", games)
	}

	// rename + flags on every game, plus one op on a non-existent game
	var ops []tools.BatchOp
	for i, g := range games {
		ops = append(ops, tools.BatchOp{Game: g, NewName: "batch game " + string(rune('A'+i))})
		ops = append(ops, tools.BatchOp{Game: g, Mask: 1 | 4})
	}
	ops = append(ops, tools.BatchOp{Game: tools.Game{Located: true, Start: 12345}, Mask: 1})
	if err := h.Batch(ctx, dev, ops, nil); err != nil {
		t.Fatal(err)
	}
	for i, op := range ops[:len(ops)-1] {
		if op.Err != nil {
			t.Errorf("op %d: %v", i, op.Err)
		}
	}
	if ops[len(ops)-1].Err == nil {
		t.Error("op on a non-existent game succeeded")
	}
	games2, total2 := mine()
	if total2 != total { // every HDL header magic still intact
		t.Fatalf("games on disk after batch: %d, want %d", total2, total)
	}
	for i, g := range games2 {
		if want := "batch game " + string(rune('A'+i)); g.Name != want || gamedb.FlagsToMask(g.Flags) != 5 {
			t.Errorf("game %d: %q flags %q", i, g.Name, g.Flags)
		}
	}

	// case-only renames (the case hdl_dump rejects with a direct modify)
	ops = ops[:0]
	for i, g := range games2 {
		ops = append(ops, tools.BatchOp{Game: g, NewName: "BATCH GAME " + string(rune('A'+i))})
	}
	if err := h.Batch(ctx, dev, ops, nil); err != nil {
		t.Fatal(err)
	}
	for i, op := range ops {
		if op.Err != nil {
			t.Errorf("case op %d: %v", i, op.Err)
		}
	}

	// collision: two games with the same ID cannot get the same name; the second
	// must fail and keep its original name
	if err := h.Inject(ctx, dev, "Batch Twin", testenv.ISOPath(t), ids[0], "", "*u4", true, nil); err != nil {
		t.Fatal(err)
	}
	games3, total3 := mine()
	var twin tools.Game
	for _, g := range games3 {
		if g.Name == "Batch Twin" {
			twin = g
		}
	}
	ops = []tools.BatchOp{{Game: twin, NewName: "batch game a"}}
	if err := h.Batch(ctx, dev, ops, nil); err != nil {
		t.Fatal(err)
	}
	if ops[0].Err == nil {
		t.Error("colliding rename succeeded")
	}
	games4, total4 := mine()
	if total4 != total3 {
		t.Fatalf("games after collision: %d, want %d", total4, total3)
	}
	for _, g := range games4 {
		if g.DirtyID {
			t.Errorf("%s: partition id has garbage after the NUL", g.Partition)
		}
		if g.Start == twin.Start && g.Name != "Batch Twin" {
			t.Errorf("twin not restored: %q", g.Name)
		}
		if g.Start == games2[0].Start && g.Name != "BATCH GAME A" {
			t.Errorf("game A changed: %q", g.Name)
		}
	}
}

// damageID rewrites a partition id as newID followed by stray bytes, the way
// some rename tools left them, keeping the header checksum valid.
func damageID(t *testing.T, dev string, start uint32, newID string) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := make([]byte, 1024)
	if _, err := f.ReadAt(h, int64(start)*512); err != nil {
		t.Fatal(err)
	}
	id := h[16:48]
	for i := range id {
		id[i] = 0
	}
	copy(id, newID)
	copy(id[len(newID)+1:], "_A1B2C3")
	var sum uint32
	for i := 4; i < 1024; i += 4 {
		sum += binary.LittleEndian.Uint32(h[i:])
	}
	binary.LittleEndian.PutUint32(h, sum)
	if _, err := f.WriteAt(h, int64(start)*512); err != nil {
		t.Fatal(err)
	}
}

func TestRepairPartitionIDs(t *testing.T) {
	h, p, dev := testenv.Tools(t)
	ctx := context.Background()
	ids := map[string]string{"SCES_900.08": "PP.SCES-90008..SAMPLE GAME 8", "SLES_900.09": "PP.SLES-90009..SAMPLE GAME 9"}
	var starts []uint32
	for id, pid := range ids {
		if err := h.Inject(ctx, dev, "Repair "+id, testenv.ISOPath(t), id, "", "*u4", true, nil); err != nil {
			t.Fatal(err)
		}
		d, _ := apa.Scan(dev)
		for _, g := range d.Games {
			if g.Startup == id {
				damageID(t, dev, g.Start, pid)
				starts = append(starts, g.Start)
			}
		}
	}
	parts, err := tools.DirtyPartitions(dev)
	if err != nil || len(parts) != len(ids) {
		t.Fatalf("damaged partitions found: %d (%v)", len(parts), err)
	}
	// the damage is real: pfsshell cannot remove such a partition
	_ = p.RmPart(ctx, dev, parts[0].ID, nil)
	if d, _ := apa.Scan(dev); !d.HasPartition(parts[0].ID) {
		t.Fatal("test setup: the damaged partition was removable, damage not reproduced")
	}
	ops := make([]tools.BatchOp, len(parts))
	for i, pt := range parts {
		ops[i] = tools.BatchOp{Game: tools.Game{Located: true, Slice: pt.Slice, Start: pt.Start, Partition: pt.ID}, CleanID: true}
	}
	if err := h.Batch(ctx, dev, ops, nil); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Err != nil {
			t.Errorf("%s: %v", op.Game.Partition, op.Err)
		}
	}
	if left, _ := tools.DirtyPartitions(dev); len(left) != 0 {
		t.Fatalf("still damaged: %+v", left)
	}
	d, _ := apa.Scan(dev)
	for _, pid := range ids {
		if !d.HasPartition(pid) {
			t.Errorf("%s: name changed by the repair", pid)
		}
		// after the repair pfsshell finds and removes it
		if err := p.RmPart(ctx, dev, pid, nil); err != nil {
			t.Errorf("rmpart %s after repair: %v", pid, err)
		}
	}
	d, _ = apa.Scan(dev)
	for _, pid := range ids {
		if d.HasPartition(pid) {
			t.Errorf("%s still present after rmpart", pid)
		}
	}
}

// fakeIcon is a minimal PS2 3D icon that passes gamedb.ValidatePS2Icon.
func fakeIcon(tag byte) []byte {
	b := make([]byte, 4096)
	binary.LittleEndian.PutUint32(b[0:], 0x00010000)
	binary.LittleEndian.PutUint32(b[4:], 1)
	binary.LittleEndian.PutUint32(b[8:], 7)
	binary.LittleEndian.PutUint32(b[16:], 3)
	b[100] = tag
	return b
}

// readHeader returns icon.sys and list.ico from the HDD-OSD header of a game.
func readHeader(t *testing.T, dev string, g tools.Game) (iconSys, listIco []byte) {
	t.Helper()
	f, err := os.Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	base := int64(g.Start)*512 + int64(g.Slice)*0x10000000*512
	b := make([]byte, 0x41000)
	if _, err := f.ReadAt(b, base); err != nil {
		t.Fatal(err)
	}
	if string(b[0x1000:0x1009]) != "PS2ICON3D" {
		t.Fatalf("no header magic: %q", b[0x1000:0x1010])
	}
	u := func(o int) int { return int(binary.LittleEndian.Uint32(b[0x1000+o:])) }
	return b[0x1000+u(0x18) : 0x1000+u(0x18)+u(0x1c)], b[0x1000+u(0x20) : 0x1000+u(0x20)+u(0x24)]
}

func TestHDDOSDHeader(t *testing.T) {
	h, p, dev := testenv.Tools(t)
	ctx := context.Background()
	const id = "SLES_990.77"
	work := t.TempDir()
	icon := filepath.Join(work, "a.ico")
	if err := os.WriteFile(icon, fakeIcon(0xA1), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := gamedb.HeaderDir(work, "Café Racer: Edition", id, icon)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.InjectWithHeader(ctx, dev, "Header Test", testenv.ISOPath(t), id, "", "*u4", true, dir, nil); err != nil {
		t.Fatal(err)
	}
	find := func() tools.Game {
		all, _, err := h.List(ctx, dev, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range all {
			if g.Startup == id {
				return g
			}
		}
		t.Fatal("game not found")
		return tools.Game{}
	}
	g := find()
	defer func() {
		if err := p.RmPart(ctx, dev, g.Partition, nil); err != nil {
			t.Error("cleanup:", err)
		}
	}()
	sys, ico := readHeader(t, dev, g)
	if !strings.Contains(string(sys), "title0 = Cafe Racer: Edition\n") || !strings.Contains(string(sys), "title1 = SLES-99077 (PAL)\n") {
		t.Fatalf("icon.sys after install:\n%s", sys)
	}
	if len(ico) != 4096 || ico[100] != 0xA1 {
		t.Fatalf("list.ico after install: %d bytes", len(ico))
	}
	// rewrite: new title only (no icon file) keeps the icon; then a new icon
	dir2, err := gamedb.HeaderDir(work, "Renamed Game", id, "")
	if err != nil {
		t.Fatal(err)
	}
	ops := []tools.BatchOp{{Game: g, HeaderDir: dir2}}
	if err := h.Batch(ctx, dev, ops, nil); err != nil || ops[0].Err != nil {
		t.Fatal(err, ops[0].Err)
	}
	sys, ico = readHeader(t, dev, g)
	if !strings.Contains(string(sys), "title0 = Renamed Game\n") || ico[100] != 0xA1 {
		t.Fatalf("after title rewrite:\n%s (icon tag %x)", sys, ico[100])
	}
	_ = os.WriteFile(icon, fakeIcon(0xB2), 0o644)
	dir3, err := gamedb.HeaderDir(work, "Renamed Game", id, icon)
	if err != nil {
		t.Fatal(err)
	}
	ops = []tools.BatchOp{{Game: g, HeaderDir: dir3}, {Game: tools.Game{Located: true, Start: 12345}, HeaderDir: dir3}}
	if err := h.Batch(ctx, dev, ops, nil); err != nil || ops[0].Err != nil || ops[1].Err == nil {
		t.Fatal(err, ops[0].Err, ops[1].Err)
	}
	if _, ico = readHeader(t, dev, g); ico[100] != 0xB2 {
		t.Fatal("icon not replaced")
	}
	if g2 := find(); g2.Name != "Header Test" || g2.Partition != g.Partition {
		t.Fatalf("game changed: %+v", g2)
	}
}
