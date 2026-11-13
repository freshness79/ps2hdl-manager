package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"strings"
	"sync"
	"sync/atomic"
)

var fetchingInfos atomic.Bool

// fetchInfos loads category / players / compatibility for all games in background.
func (a *App) fetchInfos(games []tools.Game) {
	if !fetchingInfos.CompareAndSwap(false, true) {
		return
	}
	defer fetchingInfos.Store(false)
	jobs := make(chan tools.Game)
	var wg sync.WaitGroup
	var done atomic.Int32
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				_, _ = a.info.Get(g.Startup)
				if done.Add(1)%8 == 0 {
					a.ui(func() { a.table.Refresh() })
				}
			}
		}()
	}
	for _, g := range games {
		if a.info.Cached(g.Startup) == nil {
			jobs <- g
		}
	}
	close(jobs)
	wg.Wait()
	a.ui(func() {
		a.table.Refresh()
		if a.details != nil && a.details.current != "" {
			a.updateDetailsInfo(a.details.current)
		}
	})
}

// compatSummary returns a one-line verdict and its importance.
func compatSummary(g tools.Game, gi *gamedb.GameInfo) (string, widget.Importance) {
	switch {
	case gi == nil:
		return "Loading compatibility…", widget.LowImportance
	case !gi.CompatSrc:
		return "Compatibility DB unreachable", widget.LowImportance
	case gi.ModesMask == 0 && gi.CompatKnown:
		return "Listed, no special modes", widget.SuccessImportance
	case gi.ModesMask == 0:
		return "✓ No special modes needed", widget.SuccessImportance
	case gamedb.FlagsToMask(g.Flags)&gi.ModesMask == gi.ModesMask:
		return "✓ Modes " + strings.TrimPrefix(gamedb.FlagsString(gi.ModesMask), "+") + " applied", widget.SuccessImportance
	}
	return "⚠ Needs modes " + strings.TrimPrefix(gamedb.FlagsString(gi.ModesMask), "+"), widget.WarningImportance
}

// gameInfoDialog shows the info & compatibility sheet for the selected game.
func (a *App) gameInfoDialog() {
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	go func() {
		gi, _ := a.info.Get(g.Startup)
		cfgTxt := ""
		if a.cfg.Device != "" && !a.busy.Load() {
			if c, err := a.pfs.GetCFG(context.Background(), a.cfg.Device, a.cfg.OPLPart, disc.StartupFromSerial(disc.NormalizeSerial(g.Startup))); err == nil {
				cfgTxt = c
			}
		}
		a.ui(func() { a.showInfoSheet(g, gi, cfgTxt) })
	}()
}

func (a *App) showInfoSheet(g tools.Game, gi *gamedb.GameInfo, cfgTxt string) {
	title := canvas.NewText(g.Name, colText)
	title.TextSize = 20
	title.TextStyle = fyne.TextStyle{Bold: true}
	sub := canvas.NewText(fmt.Sprintf("%s  ·  %s  ·  %.2f GB", g.Startup, g.Type, float64(g.SizeKB)/1024/1024), colMuted)
	sub.TextSize = 12

	row := func(k, v string) *widget.FormItem {
		l := widget.NewLabel(dash(v))
		l.Wrapping = fyne.TextWrapWord
		return widget.NewFormItem(k, l)
	}
	info := widget.NewForm(
		row("Category", gi.Genre),
		row("Players", map[bool]string{true: fmt.Sprintf("%d", gi.Players), false: ""}[gi.Players > 0]),
		row("Developer", gi.Developer),
		row("Publisher", gi.Publisher),
		row("Release", gi.Release),
		row("Rating", map[bool]string{true: gi.Rating + " / 5", false: ""}[gi.Rating != ""]),
		row("Video", joinNonEmpty(" · ", gamedb.VmodeText(gi.Vmode), gamedb.AspectText(gi.Aspect), gamedb.ScanText(gi.Scan))),
	)
	desc := widget.NewLabel(dash(gi.Description))
	desc.Wrapping = fyne.TextWrapWord

	// compatibility sheet
	verdict, imp := compatSummary(g, gi)
	cfgMask := atoiSafe(gamedb.ParseCFG(cfgTxt)["$Compatibility"])
	if gi.ModesMask != 0 && cfgMask&gi.ModesMask == gi.ModesMask {
		hdr := gamedb.FlagsToMask(g.Flags)&gi.ModesMask == gi.ModesMask
		verdict, imp = "✓ Modes set in OPL CFG", widget.SuccessImportance
		if !hdr {
			verdict += " (HD Loader header flags missing)"
		}
	}
	vl := widget.NewLabelWithStyle(verdict, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	vl.Importance = imp
	var lines []string
	if gi.ModesMask != 0 {
		for _, n := range gi.Modes() {
			lines = append(lines, fmt.Sprintf("  Mode %d — %s", n, gamedb.ModeNames[n]))
		}
	} else if gi.CompatSrc {
		lines = append(lines, "  The OPL compatibility DB lists no required modes for this ID:", "  default settings should work.")
	}
	cur := dash(g.Flags)
	lines = append(lines, "",
		"  HDL header flags on disk (HD Loader):  "+cur,
		"  DMA mode on disk:                        "+dash(g.DMA))
	cfgKV := gamedb.ParseCFG(cfgTxt)
	switch {
	case cfgTxt == "":
		lines = append(lines, "  OPL CFG ("+a.cfg.OPLPart+"/"+tools.CfgDir(a.cfg.OPLPart)+"):  none")
	default:
		c := cfgKV["$Compatibility"]
		if c == "" {
			c = "0"
		}
		lines = append(lines, "  OPL CFG $Compatibility:  "+c+"  ("+dash(gamedb.ModesDescription(atoiSafe(c)))+")")
	}
	lines = append(lines, "",
		"  OPL reads the modes from CFG/<ID>.cfg on "+a.cfg.OPLPart+"; HD Loader from the HDL header.",
		"  “Apply” writes both, plus the game info shown by OPL.")
	det := widget.NewLabel(strings.Join(lines, "\n"))
	det.TextStyle = fyne.TextStyle{Monospace: true}

	var d dialog.Dialog
	apply := widget.NewButton("Apply recommended modes & info", func() {
		d.Hide()
		go a.applyCompat([]tools.Game{g}, false)
	})
	apply.Importance = widget.HighImportance
	content := container.NewVBox(
		container.NewVBox(title, sub), hairline(),
		sectionLabel("GAME"), info,
		sectionLabel("DESCRIPTION"), desc,
		sectionLabel("COMPATIBILITY (OPL / HD LOADER)"), vl, det,
		container.NewHBox(layout.NewSpacer(), apply),
	)
	sc := container.NewVScroll(content)
	sc.SetMinSize(fyne.NewSize(760, 560))
	d = dialog.NewCustom("Game info", "Close", sc, a.win)
	d.Show()
}

func atoiSafe(s string) int {
	n := 0
	fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n
}

// applyCompatAll applies recommended modes and game info to every game.
func (a *App) applyCompatAll() {
	if a.cfg.Device == "" {
		a.ui(func() { a.needReady() })
		return
	}
	games := a.gamesSnapshot()
	need := 0
	for _, g := range games {
		gi, _ := a.info.Get(g.Startup)
		if gi.ModesMask != 0 && gamedb.FlagsToMask(g.Flags)&gi.ModesMask != gi.ModesMask {
			need++
		}
	}
	msg := fmt.Sprintf("%d of %d games need compatibility modes that are not set yet.\n\n"+
		"Apply will:\n• set the HDL header flags (HD Loader) with hdl_dump\n", need, len(games))
	msg += "• write/merge CFG/<ID>.cfg on " + a.cfg.OPLPart + " ($Compatibility for OPL + game info)\n"
	a.ui(func() {
		dialog.ShowConfirm("Apply compatibility modes", msg, func(ok bool) {
			if ok {
				go a.applyCompat(games, false)
			}
		}, a.win)
	})
}

// applyCompat sets header flags + OPL CFG for games. quiet = no final dialog (post-install).
func (a *App) applyCompat(games []tools.Game, quiet bool) {
	if !quiet {
		if !a.beginWrite("Applying compatibility modes…", "Applying compatibility modes") {
			return
		}
		defer a.busy.Store(false)
	}
	ctx := context.Background()
	flagsN, cfgN, errN := 0, 0, 0
	type item struct {
		g  tools.Game
		gi *gamedb.GameInfo
		st string
	}
	var items []item
	ids := make([]string, 0, len(games))
	for _, g := range games {
		ids = append(ids, g.Startup)
	}
	a.prefetch(ids, fetchInfo) // compatibility info for all games in parallel
	var flagOps []tools.BatchOp
	for _, g := range games {
		gi, _ := a.info.Get(g.Startup)
		cur := gamedb.FlagsToMask(g.Flags)
		if gi.ModesMask != 0 && cur&gi.ModesMask != gi.ModesMask {
			flagOps = append(flagOps, tools.BatchOp{Game: g, Mask: cur | gi.ModesMask})
		}
	}
	batched := false
	if len(flagOps) > 0 {
		bg := make([]tools.Game, len(flagOps))
		for i := range flagOps {
			bg[i] = flagOps[i].Game
		}
		if a.hdl.CanBatch(bg...) { // all header flags in one hdl_dump process
			a.setStatus(fmt.Sprintf("Setting HDL flags on %d games…", len(flagOps)))
			if err := a.hdl.Batch(ctx, a.cfg.Device, flagOps, nil); err != nil {
				a.log("Flags batch failed: " + err.Error())
				errN += len(flagOps)
			} else {
				for _, op := range flagOps {
					if op.Err != nil {
						a.log("Flags not set for " + op.Game.Name + ": " + op.Err.Error())
						errN++
					} else {
						a.log(fmt.Sprintf("%s: HDL flags %s", op.Game.Name, gamedb.FlagsString(op.Mask)))
						flagsN++
					}
				}
			}
			batched = true
		}
	}
	for i, g := range games {
		gi, _ := a.info.Get(g.Startup)
		items = append(items, item{g, gi, disc.StartupFromSerial(disc.NormalizeSerial(g.Startup))})
		cur := gamedb.FlagsToMask(g.Flags)
		if !batched && gi.ModesMask != 0 && cur&gi.ModesMask != gi.ModesMask {
			a.setStatus(fmt.Sprintf("Setting flags %d/%d: %s", i+1, len(games), g.Name))
			if _, err := a.hdl.Run(ctx, a.log, "modify", a.cfg.Device, g.Name, gamedb.FlagsString(cur|gi.ModesMask)); err != nil {
				a.log("Flags not set for " + g.Name + ": " + err.Error())
				errN++
			} else {
				a.log(fmt.Sprintf("%s: HDL flags %s", g.Name, gamedb.FlagsString(cur|gi.ModesMask)))
				flagsN++
			}
		}
	}
	if len(items) > 0 {
		a.setStatus("Writing OPL CFG files…")
		{ // GetCFGs + PutCFGs: two pfsshell sessions in total (PutCFGs creates +OPL if needed)
			// read all existing CFGs in one pfsshell session
			names := make([]string, 0, len(items))
			for _, it := range items {
				if it.st != "" && disc.NormalizeSerial(it.st) != "" {
					names = append(names, it.st+".cfg")
				}
			}
			existing, err := a.pfs.GetCFGs(ctx, a.cfg.Device, a.cfg.OPLPart, names)
			if err != nil {
				a.log("Reading CFGs: " + err.Error())
			}
			out := map[string]string{}
			for _, it := range items {
				if disc.NormalizeSerial(it.st) == "" {
					continue
				}
				name := it.st + ".cfg"
				old := gamedb.ParseCFG(existing[name])
				nw := gamedb.MergeGameCFG(old, it.g.Name, it.gi, true)
				if gamedb.FormatCFG(nw) != gamedb.FormatCFG(old) {
					out[name] = gamedb.FormatCFG(nw)
				}
			}
			if err := a.pfs.PutCFGs(ctx, a.cfg.Device, a.cfg.OPLPart, out, a.log); err != nil {
				a.log("Writing CFGs: " + err.Error())
				errN++
			} else {
				cfgN = len(out)
			}
		}
	}
	summary := fmt.Sprintf("HDL flags set: %d · OPL CFG written: %d · errors: %d", flagsN, cfgN, errN)
	a.log("Compatibility: " + summary)
	a.setStatus(summary)
	if !quiet {
		a.ui(func() { dialog.ShowInformation("Compatibility", strings.ReplaceAll(summary, " · ", "\n"), a.win) })
	}
	if flagsN > 0 {
		if quiet {
			return // caller refreshes
		}
		a.busy.Store(false)
		a.refreshGames()
	}
}

// writeInfoAll writes the OPL info page data (genre, developer, release, video
// mode, …) of every game into its CFG, leaving compatibility modes alone.
func (a *App) writeInfoAll() {
	if !a.needReady() {
		return
	}
	games := a.gamesSnapshot()
	if len(games) == 0 {
		return
	}
	dialog.ShowConfirm("OPL game info",
		fmt.Sprintf("Write the info page data shown by OPL (category, players, developer, publisher, release, rating, "+
			"video mode, aspect, scan, description) into CFG/<ID>.cfg on %s for all %d games?\n\n"+
			"Valid values already there and compatibility modes are kept.", a.cfg.OPLPart, len(games)),
		func(ok bool) {
			if ok {
				go a.writeInfo(games)
			}
		}, a.win)
}

func (a *App) writeInfo(games []tools.Game) {
	if !a.beginWrite("Writing OPL game info…", "Writing OPL game info") {
		return
	}
	ctx := context.Background()
	ids := make([]string, 0, len(games))
	for _, g := range games {
		ids = append(ids, g.Startup)
	}
	a.setStatus(fmt.Sprintf("Fetching info for %d games…", len(ids)))
	a.prefetch(ids, fetchInfo)
	var names []string
	seen := map[string]bool{}
	for _, g := range games {
		if n := disc.NormalizeSerial(g.Startup); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, disc.StartupFromSerial(n)+".cfg")
		}
	}
	a.setStatus("Reading OPL CFG files…")
	existing, err := a.pfs.GetCFGs(ctx, a.cfg.Device, a.cfg.OPLPart, names)
	if err != nil {
		a.end("Error")
		a.showErr(err)
		return
	}
	out := map[string]string{}
	done := map[string]bool{}
	for _, g := range games {
		n := disc.NormalizeSerial(g.Startup)
		if n == "" || done[n] {
			continue
		}
		done[n] = true
		name := disc.StartupFromSerial(n) + ".cfg"
		gi, _ := a.info.Get(g.Startup)
		old := gamedb.ParseCFG(existing[name])
		nw := gamedb.MergeGameCFG(old, g.Name, gi, false)
		if gamedb.FormatCFG(nw) != gamedb.FormatCFG(old) {
			out[name] = gamedb.FormatCFG(nw)
		}
	}
	a.setStatus(fmt.Sprintf("Writing %d CFG files…", len(out)))
	err = a.pfs.PutCFGs(ctx, a.cfg.Device, a.cfg.OPLPart, out, a.log)
	msg := fmt.Sprintf("OPL game info: %d CFG files updated, %d already complete", len(out), len(names)-len(out))
	if err != nil {
		a.end("Error")
		a.showErr(err)
		return
	}
	a.end(msg)
	a.log(msg)
	a.ui(func() { dialog.ShowInformation("OPL game info", msg, a.win) })
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
