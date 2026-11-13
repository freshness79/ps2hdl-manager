package ui

import (
	"fyne.io/fyne/v2/test"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"os"
	"strings"
	"testing"
)

// Clicking an activity line copies it to the clipboard.
func TestActivityCopy(t *testing.T) {
	fa := test.NewTempApp(t)
	a := &App{cfg: &config.Config{OPLPart: "+OPL"}, db: gamedb.NewTitleDB(), selected: -1, fa: fa}
	a.info = gamedb.NewInfoStore(a.db)
	a.hdl, a.pfs = tools.EmbeddedHDL(), tools.EmbeddedPFS()
	a.win = test.NewWindow(nil)
	a.build()
	a.logLines = []string{"12:00:01 first line", "12:00:02 ERROR SLUS_900.02: something failed"}
	a.logList.Refresh()
	a.logList.Select(1)
	if got := fa.Clipboard().Content(); got != a.logLines[1] {
		t.Fatalf("clipboard = %q", got)
	}
	if a.status.Text == "Ready" {
		t.Error("no feedback in the status bar")
	}
}

// Write operations are refused up front (with an explanation) without access.
func TestNeedWriteBlocks(t *testing.T) {
	fa := test.NewTempApp(t)
	a := &App{cfg: &config.Config{OPLPart: "+OPL", Device: "/dev/does-not-exist"}, db: gamedb.NewTitleDB(), selected: -1, fa: fa}
	a.info = gamedb.NewInfoStore(a.db)
	a.hdl, a.pfs = tools.EmbeddedHDL(), tools.EmbeddedPFS()
	a.win = test.NewWindow(nil)
	a.build()
	if a.beginWrite("Renaming…", "Renaming a game") {
		t.Fatal("write operation started without write access")
	}
	if a.busy.Load() {
		t.Error("app left busy")
	}
	found := false
	for _, l := range a.logLines {
		found = found || strings.Contains(l, "no write access")
	}
	if !found {
		t.Error("no log line", a.logLines)
	}
}

// A file that cannot be installed is explained in the activity log: a headline
// with file and format, followed by the details (what was tried, what it is).
func TestProblemDetailsInActivity(t *testing.T) {
	fa := test.NewTempApp(t)
	a := &App{cfg: &config.Config{OPLPart: "+OPL", Device: "/dev/null"}, db: gamedb.NewTitleDB(), selected: -1, fa: fa}
	a.info = gamedb.NewInfoStore(a.db)
	a.hdl, a.pfs = tools.EmbeddedHDL(), tools.EmbeddedPFS()
	a.win = test.NewWindow(nil)
	a.build()
	in := a.openInstaller(nil)
	dir := t.TempDir()
	zip := dir + "/Game.iso"
	_ = os.WriteFile(zip, append([]byte("PK\x03\x04"), make([]byte, 80000)...), 0o644)
	sub := dir + "/Lonely.sub"
	_ = os.WriteFile(sub, make([]byte, 9600), 0o644)
	in.addPath(zip)
	in.addPath(sub)
	all := strings.Join(a.logLines, "\n")
	for _, want := range []string{
		"ERROR Game.iso [ISO]: cannot read the image", "ZIP archive", "  - 2048-byte sectors (ISO)", // what was tried
		"Size: 80004 bytes",
		"ERROR Lonely.sub [CCD/IMG/SUB (CloneCD)]", "the .ccd and .img with the same name are needed",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("activity log lacks %q:\n%s", want, all)
		}
	}
}
