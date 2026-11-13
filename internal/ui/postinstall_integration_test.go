package ui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/testenv"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
)

// postInstall puts the configured OPL art types as PNG (removing the old JPG
// cover) and writes the full info sheet into the CFG. Needs REAL_DEV.
func TestPostInstallArtAndInfo(t *testing.T) {
	h, p, dev := testenv.Tools(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	const id = "SLUS_990.55"

	fa := test.NewTempApp(t)
	a := &App{cfg: &config.Config{OPLPart: "+OPL", Device: dev, ArtKinds: []string{"COV", "BG"},
		ArtArchive: "-", IconIndex: "-", CoverURL: "http://127.0.0.1:1/{ID}.jpg"}, db: gamedb.NewTitleDB(), selected: -1, fa: fa}
	a.info = gamedb.NewInfoStore(a.db)
	a.hdl, a.pfs = h, p
	a.win = test.NewWindow(nil)
	a.build()

	pic := func(w, hh int) []byte {
		var b bytes.Buffer
		_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, w, hh)))
		return b.Bytes()
	}
	for k, b := range map[string][]byte{"COV": pic(140, 200), "BG": pic(640, 480), "SCR": pic(250, 188)} {
		if err := os.WriteFile(filepath.Join(gamedb.ArtCacheDir(), gamedb.ArtFileName(id, k)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// an old JPG cover on the disk, as earlier versions wrote it
	old := t.TempDir()
	_ = os.WriteFile(filepath.Join(old, id+"_COV.jpg"), []byte("jpg"), 0o644)
	if err := p.PutArt(ctx, dev, "+OPL", old, []string{id + "_COV.jpg"}, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = p.RmArt(ctx, dev, "+OPL", gamedb.AllArtNames(id), nil)
		_ = p.PutCFGs(ctx, dev, "+OPL", map[string]string{id + ".cfg": ""}, nil)
	}()
	a.info.Put(id, &gamedb.GameInfo{Genre: "Action", Players: 2, Developer: "Dev", Publisher: "Pub",
		Release: "10-26-2004", Vmode: "ntsc", Aspect: "s", Scan: "480i", ModesMask: 4})

	cfgN, artN, err := a.postInstall([]tools.Game{{Startup: id, Name: "Post Install Test"}}, true, true)
	if err != nil || cfgN != 1 || artN != 1 {
		t.Fatal(cfgN, artN, err)
	}
	files, err := p.ListArt(ctx, dev, "+OPL")
	if err != nil {
		t.Fatal(err)
	}
	on := strings.Join(files, " ")
	for _, want := range []string{id + "_COV.png", id + "_BG.png"} {
		if !strings.Contains(on, want) {
			t.Errorf("%s missing: %s", want, on)
		}
	}
	for _, not := range []string{id + "_COV.jpg", id + "_SCR.png"} {
		if strings.Contains(on, not) {
			t.Errorf("%s present: %s", not, on)
		}
	}
	cfg, err := p.GetCFG(ctx, dev, "+OPL", id)
	if err != nil {
		t.Fatal(err)
	}
	kv := gamedb.ParseCFG(cfg)
	for k, v := range map[string]string{"Title": "Post Install Test", "Publisher": "Pub", "Vmode": "vmode/ntsc",
		"VmodeText": "NTSC", "Aspect": "aspect/s", "ScanText": "480i", "Players": "players/2", "$Compatibility": "4"} {
		if kv[k] != v {
			t.Errorf("CFG %s = %q, want %q\n%s", k, kv[k], v, cfg)
		}
	}
}
