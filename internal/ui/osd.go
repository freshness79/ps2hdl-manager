package ui

// HDD-OSD (Browser 2.0) titles and icons, and the picture sources for OPL art.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"fyne.io/fyne/v2/dialog"

	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
)

// iconDB returns the HDD-OSD icon source for the configured index (nil when off).
func (a *App) iconDB() *gamedb.IconDB {
	a.srcMu.Lock()
	defer a.srcMu.Unlock()
	u := a.cfg.IconIndexURL()
	if u == "" {
		return nil
	}
	if a.icons == nil || a.icons.IndexURL != u {
		a.icons = gamedb.NewIconDB(u)
	}
	return a.icons
}

// artArchive returns the OPL Manager art backup (nil when off).
func (a *App) artArchive() *gamedb.OPLMArt {
	a.srcMu.Lock()
	defer a.srcMu.Unlock()
	u := a.cfg.ArtArchiveURL()
	if u == "" {
		return nil
	}
	if a.oplm == nil || a.oplm.URL != u {
		a.oplm = gamedb.NewOPLMArt(u)
	}
	return a.oplm
}

// artKeys are the OPL art types to install.
func (a *App) artKeys() []string {
	if a.cfg.ArtKinds == nil {
		return gamedb.DefaultArtKeys()
	}
	var out []string
	for _, k := range a.cfg.ArtKinds {
		if _, ok := gamedb.ArtTypeByKey(k); ok {
			out = append(out, strings.ToUpper(k))
		}
	}
	return out
}

// problemLog collects source problems from parallel workers and logs each
// distinct one once, with the number of games it hit.
type problemLog struct {
	n     map[string]int
	order []string
}

func (p *problemLog) add(msg string) {
	if p.n == nil {
		p.n = map[string]int{}
	}
	if p.n[msg] == 0 {
		p.order = append(p.order, msg)
	}
	p.n[msg]++
}

func (p *problemLog) flush(log func(string)) {
	for _, m := range p.order {
		if c := p.n[m]; c > 1 {
			log(fmt.Sprintf("%s (%d games)", m, c))
		} else {
			log(m)
		}
	}
	p.n, p.order = nil, nil
}

// osdIcon returns the cached HDD-OSD icon of a game ("" when none).
func (a *App) osdIcon(id string, probs *problemLog) string {
	db := a.iconDB()
	if db == nil || disc.NormalizeSerial(id) == "" {
		return ""
	}
	p, err := db.Icon(id)
	if err != nil && !errors.Is(err, gamedb.ErrNoArt) && probs != nil {
		probs.add("HDD-OSD icon: " + err.Error())
	}
	return p
}

// headerDir prepares icon.sys (title shown by the HDD-OSD) and list.ico for a
// game in a new folder under parent; "" when HDD-OSD data is disabled.
func (a *App) headerDir(parent, title, id string) string {
	if !a.cfg.OSDEnabled() {
		return ""
	}
	var probs problemLog
	icon := a.osdIcon(id, &probs)
	probs.flush(a.log)
	dir, err := gamedb.HeaderDir(parent, title, id, icon)
	if err != nil {
		a.log("HDD-OSD data not prepared: " + err.Error())
		return ""
	}
	if icon == "" {
		a.log("HDD-OSD: no 3D icon for " + id + " (title only, standard icon)")
	}
	return dir
}

// writeOSD rewrites the HDD-OSD title and icon of installed games in one batch.
// Returns how many were updated.
func (a *App) writeOSD(games []tools.Game) (int, error) {
	if len(games) == 0 {
		return 0, nil
	}
	if !a.hdl.CanBatch(games...) {
		return 0, errors.New("the partition table could not be read directly: refresh the game list and retry")
	}
	tmp, err := os.MkdirTemp("", "ps2hdl-osd")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	ops, noIcon, err := a.osdOps(tmp, games)
	if err != nil {
		return 0, err
	}
	a.setStatus(fmt.Sprintf("Writing HDD-OSD data of %d games…", len(ops)))
	if err := a.hdl.Batch(context.Background(), a.cfg.Device, ops, nil); err != nil {
		return 0, err
	}
	n := 0
	var failed []string
	for _, op := range ops {
		if op.Err != nil {
			failed = append(failed, op.Game.Name+": "+op.Err.Error())
		} else {
			n++
		}
	}
	sort.Strings(failed)
	for _, f := range failed {
		a.log("HDD-OSD data not written for " + f)
	}
	a.log(fmt.Sprintf("HDD-OSD: %d games updated (%d with the standard icon)", n, noIcon))
	if len(failed) > 0 {
		return n, fmt.Errorf("%d games failed (see the activity log)", len(failed))
	}
	return n, nil
}

// osdOps prepares batch ops that rewrite the HDD-OSD data of games (with their
// current or new names in Name); noIcon counts games without a 3D icon.
func (a *App) osdOps(tmp string, games []tools.Game) (ops []tools.BatchOp, noIcon int, err error) {
	ids := make([]string, 0, len(games))
	for _, g := range games {
		ids = append(ids, g.Startup)
	}
	a.prefetch(ids, fetchIcons)
	for _, g := range games {
		icon := a.osdIcon(g.Startup, nil)
		if icon == "" {
			noIcon++
		}
		dir, err := gamedb.HeaderDir(tmp, g.Name, g.Startup, icon)
		if err != nil {
			return nil, 0, err
		}
		ops = append(ops, tools.BatchOp{Game: g, HeaderDir: dir})
	}
	return ops, noIcon, nil
}

// osdAfterRename updates the HDD-OSD title of renamed games (Name = new name)
// when HDD-OSD data is on; problems are only logged.
func (a *App) osdAfterRename(renamed []tools.Game) {
	if !a.cfg.OSDEnabled() || len(renamed) == 0 || !a.hdl.CanBatch(renamed...) {
		return
	}
	if _, err := a.writeOSD(renamed); err != nil {
		a.log("HDD-OSD title not updated: " + err.Error())
	}
}

// osdAll rewrites the HDD-OSD title and icon of every installed game.
func (a *App) osdAll() {
	if !a.needReady() {
		return
	}
	games := a.gamesSnapshot()
	if len(games) == 0 {
		return
	}
	dialog.ShowConfirm("HDD-OSD titles & icons",
		fmt.Sprintf("Write the HDD-OSD (Browser 2.0) title and 3D icon of all %d games?\n\n"+
			"Title line 1: the game name · line 2: its ID and region.\n"+
			"Games without an icon in the icon pack keep their current one.", len(games)),
		func(ok bool) {
			if ok {
				go a.runOSD(games)
			}
		}, a.win)
}

// osdSelected rewrites the HDD-OSD title and icon of the selected game.
func (a *App) osdSelected() {
	if !a.needReady() {
		return
	}
	if g, ok := a.selectedGame(); ok {
		go a.runOSD([]tools.Game{g})
	}
}

func (a *App) runOSD(games []tools.Game) {
	if !a.beginWrite("Writing HDD-OSD titles & icons…", "Writing HDD-OSD data") {
		return
	}
	n, err := a.writeOSD(games)
	a.end(fmt.Sprintf("HDD-OSD data written for %d games", n))
	if err != nil {
		a.showErr(err)
	}
}
