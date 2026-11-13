//go:build shot

package ui

import (
	"errors"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	legal "github.com/freshness79/ps2hdl-manager"
	chdlib "github.com/freshness79/ps2hdl-manager/internal/chd"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/system"
	"github.com/freshness79/ps2hdl-manager/internal/testenv"
	"github.com/freshness79/ps2hdl-manager/internal/testenv/sample"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// go test -tags shot -run TestShot: software-rendered windows, written to
// SHOT_OUT (make shot: bin/shots) or a temporary folder.
// With SHOT_DOCS=<dir> (make docs-images) clean showcase versions are also
// written to <dir> for the README: no sample errors, no repair button.
func TestShot(t *testing.T) {
	docs := os.Getenv("SHOT_DOCS")
	out := os.Getenv("SHOT_OUT")
	if out == "" {
		out = t.TempDir()
	}
	_ = os.MkdirAll(out, 0o755)
	shot := func(w fyne.Window, dev, doc string) {
		save(w, filepath.Join(out, filepath.Base(dev)))
		if docs != "" && doc != "" {
			save(w, docs+"/"+doc)
		}
	}
	fa := test.NewTempApp(t)
	fa.Settings().SetTheme(slickTheme{})
	a := &App{cfg: &config.Config{OPLPart: "+OPL"}, db: gamedb.NewTitleDB(), selected: -1, fa: fa}
	a.info = gamedb.NewInfoStore(a.db)
	a.hdl, a.pfs = tools.EmbeddedHDL(), tools.EmbeddedPFS()
	a.win = test.NewWindow(nil)
	a.win.Resize(fyne.NewSize(1440, 860))
	a.build()
	games := []tools.Game{
		// placeholder games: invented titles and IDs, no real game appears in the screenshots
		{Type: "DVD", Startup: "SLUS_999.01", SizeKB: 4000000, Flags: "", DMA: "*u4", Name: "Sample Game One"},
		{Type: "DVD", Startup: "SLUS_999.02", SizeKB: 4400000, Flags: "+1+3", DMA: "*u4", Name: "Sample Game Two: Extended Title"},
		{Type: "DVD", Startup: "SCES_999.03", SizeKB: 1600000, Flags: "+2+3+6", DMA: "*u4", Name: "Sample Game Three"},
		{Type: "CD", Startup: "SLUS_999.04", SizeKB: 600000, Flags: "", DMA: "*u4", Name: "Sample Game Four"},
		{Type: "DVD", Startup: "SLES_999.05", SizeKB: 3800000, Flags: "", DMA: "*u4", Name: "Sample Game Five"},
		{Type: "DVD", Startup: "SLUS_999.06", SizeKB: 2100000, Flags: "", DMA: "*u4", Name: "Sample Game Six"},
	}
	infos := []*gamedb.GameInfo{
		{Genre: "Survival Horror", Players: 1, CompatKnown: true, CompatSrc: true},
		{Genre: "Action", Players: 1, ModesMask: 5, CompatKnown: true, CompatSrc: true, Developer: "Example Studio", Publisher: "Example Publisher", Release: "10-26-2004", Vmode: "ntsc", Aspect: "s", Scan: "480i"},
		{Genre: "Platformer", Players: 1, ModesMask: 38, CompatKnown: true, CompatSrc: true, Developer: "Placeholder Studio", Publisher: "Example Publisher", Release: "12-04-2001", Rating: "4", Vmode: "pal", Aspect: "s", Scan: "480i",
			Description: "Placeholder description: this text stands in for the game description taken from the online info sheet."},
		{Genre: "Sports", Players: 2, CompatSrc: true},
		{Genre: "Adventure", Players: 1, CompatSrc: true},
		{Genre: "Racing", Players: 6, ModesMask: 1, CompatKnown: true, CompatSrc: true},
	}
	for i, g := range games {
		a.info.Put(g.Startup, infos[i])
	}
	a.games = games
	a.applyFilter()
	a.devSelect.Options = []string{"/dev/disk4  (500 GB, PS2 APA)"}
	a.devSelect.Selected = "/dev/disk4  (500 GB, PS2 APA)"
	a.devSelect.Refresh()
	a.hdl.Version = "0.9.2"
	a.updateToolInfo()
	a.status.SetText("Ready")
	a.tGames.Set("6", "5 DVD · 1 CD")
	a.tSize.Set("15.7 GB", "on /dev/disk4")
	a.tDB.Set("12,431", "titles · updated today")
	a.usageVal.Text, a.usageSub.Text = "21%", "98 GB used of 465 GB"
	a.usage.Set(0.21)
	lines := []string{"Disk /dev/disk4: 6 games", "SLUS_999.02: \"SAMPLE 2\" → \"Sample Game Two: Extended Title\"", "WARNING PP.SLES-99905..SAMPLE_GAME_FIVE: partition id damaged (example)", "ERROR SLES_999.05: example error line", "Compatibility: HDL flags set: 2 · OPL CFG written: 3 · errors: 0"}
	if docs != "" {
		lines = []string{"15:02:11 Disk /dev/disk4: 6 games · 98 GB used of 465 GB", "15:02:40 Installed Sample Game Six (DVD, 2.00 GB) · cover · OPL CFG",
			"15:03:05 SLUS_999.02: \"SAMPLE 2\" → \"Sample Game Two: Extended Title\"", "15:03:12 Compatibility: HDL flags set: 2 · OPL CFG written: 3 · errors: 0"}
	}
	a.logLines = append(a.logLines, lines...)
	a.logList.Refresh()
	if docs == "" {
		a.updateRepairButton(4)
	}
	a.accessBtn.SetText("Read & write")
	a.accessBtn.SetIcon(theme.ConfirmIcon())
	a.accessBtn.Importance = widget.SuccessImportance
	a.accessBtn.Show()
	a.headerBar.Refresh()
	a.table.Select(widget.TableCellID{Row: 1, Col: 1})
	shot(a.win, "shot.png", "main-window.png")

	a.cfg.Device = "/dev/disk4"
	in := a.openInstaller(nil)
	in.w.Resize(fyne.NewSize(1250, 620))
	iso := sample.ISO()
	dir := t.TempDir()
	good := sample.Path(t, "Sample Game One (USA).iso", nil)
	extra := map[string][]byte{"Sample Game Seven (Europe).nrg": testenv.NRGv2DAO(testenv.ToRaw(iso, 2), 2352), "Sample Game Eight.bin": testenv.ToRaw(iso, 2), "Store Package.pkg": append([]byte("\x7fPKG"), make([]byte, 64)...)}
	var paths []string
	for n, b := range extra {
		_ = os.WriteFile(dir+"/"+n, b, 0o644)
		paths = append(paths, dir+"/"+n)
	}
	chd := dir + "/Sample Game Nine (Europe).chd"
	_ = chdlib.CreateDVD(good, chd, chdlib.WriteOptions{}, nil)
	paths = append(paths, chd)
	for _, p := range append([]string{good, sample.Path(t, "SLUS_999.01.Sample Game One.iso", nil), sample.Path(t, "Broken Image.iso", sample.BrokenPathTable())}, paths...) {
		in.addPath(p)
	}
	time.Sleep(3 * time.Second)
	shot(in.w, "shot_inst.png", "installer.png")
	a.showInfoSheet(games[2], infos[2], "")
	shot(a.win, "shot_info.png", "game-info.png")

	a2 := &App{cfg: a.cfg, db: a.db, info: a.info, selected: -1, fa: fa, hdl: a.hdl, pfs: a.pfs}
	a2.win = test.NewWindow(nil)
	a2.win.Resize(fyne.NewSize(1440, 860))
	a2.build()
	a2.about()
	shot(a2.win, "shot_about.png", "about.png")

	a3 := &App{cfg: a.cfg, db: a.db, info: a.info, selected: -1, fa: fa, hdl: a.hdl, pfs: a.pfs}
	a3.win = test.NewWindow(nil)
	a3.win.Resize(fyne.NewSize(1440, 860))
	a3.build()
	a3.accessBtn.SetText("Read only")
	a3.accessBtn.SetIcon(theme.WarningIcon())
	a3.accessBtn.Importance = widget.WarningImportance
	a3.accessBtn.Show()
	a3.headerBar.Refresh()
	acc := system.Access{Device: "/dev/disk4", Path: "/dev/disk4", Read: true}
	acc.Hint = "macOS lets only administrators access disks directly.\nQuit the app and start it from Terminal with:\n\n    sudo \"/Applications/PS2 HDL Manager.app/Contents/MacOS/ps2hdl-manager\""
	acc.WErr = errors.New("open /dev/disk4: permission denied")
	a3.accessDialog(acc, "Renaming a game")
	shot(a3.win, "shot_access.png", "disk-access.png")
	a2.licenseWindow("Third-party software notices", legal.ThirdPartyNotices)
	for _, w := range fa.Driver().AllWindows() {
		if w.Title() == "Third-party software notices" {
			w.Resize(fyne.NewSize(900, 700))
			save(w, filepath.Join(out, "shot_lic.png"))
			_ = w
		}
	}
}

func save(w fyne.Window, p string) {
	f, _ := os.Create(p)
	defer f.Close()
	_ = png.Encode(f, w.Canvas().Capture())
}
