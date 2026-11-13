package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2/dialog"
	"github.com/freshness79/ps2hdl-manager/internal/apa"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"strings"
)

// repairPartitionIDs asks for confirmation and cleans every damaged id in one
// hdl_dump batch process. Names, game data and settings are not changed.
func (a *App) repairPartitionIDs() {
	if !a.needReady() {
		return
	}
	dev := a.cfg.Device
	go func() {
		parts, err := tools.DirtyPartitions(dev)
		if err != nil {
			a.showErr(err)
			return
		}
		if len(parts) == 0 {
			a.ui(func() {
				dialog.ShowInformation("Repair partition IDs", "No damaged partition IDs on "+dev+".", a.win)
			})
			a.updateRepairButton(0)
			return
		}
		var names []string
		for i, p := range parts {
			if i == 12 {
				names = append(names, fmt.Sprintf("… and %d more", len(parts)-i))
				break
			}
			names = append(names, "  "+p.ID)
		}
		msg := fmt.Sprintf("%d partition IDs on %s contain leftover bytes after the name\n"+
			"(typically left by older rename tools). The PS2 and pfsshell may fail to find them.\n\n%s\n\n"+
			"The repair only clears the unused bytes of each ID: names, game data and\n"+
			"settings are not changed.", len(parts), dev, strings.Join(names, "\n"))
		a.ui(func() {
			dialog.ShowConfirm("Repair partition IDs", msg, func(ok bool) {
				if ok {
					go a.doRepairPartitionIDs(dev, parts)
				}
			}, a.win)
		})
	}()
}

func (a *App) doRepairPartitionIDs(dev string, parts []apa.Partition) {
	if !a.beginWrite("Repairing partition IDs…", "Repairing partition IDs") {
		return
	}
	ops := make([]tools.BatchOp, len(parts))
	for i, p := range parts {
		ops[i] = tools.BatchOp{Game: tools.Game{Located: true, Slice: p.Slice, Start: p.Start, Partition: p.ID}, CleanID: true}
	}
	err := a.hdl.Batch(context.Background(), dev, ops, func(done int) {
		a.ui(func() { a.progress.SetValue(float64(done) / float64(len(ops))) })
	})
	okN, errN := 0, 0
	if err != nil {
		errN = len(ops)
		a.log("ERROR repairing partition IDs: " + err.Error())
	} else {
		for _, op := range ops {
			if op.Err != nil {
				errN++
				a.log("ERROR " + op.Game.Partition + ": " + op.Err.Error())
			} else {
				okN++
				a.log("Repaired partition ID: " + op.Game.Partition)
			}
		}
	}
	left, verr := tools.DirtyPartitions(dev) // verify on disk
	a.end(fmt.Sprintf("Partition IDs repaired: %d, errors: %d", okN, errN))
	summary := fmt.Sprintf("Repaired: %d\nErrors: %d", okN, errN)
	switch {
	case verr != nil:
		summary += "\n\nCould not verify: " + verr.Error()
	case len(left) > 0:
		summary += fmt.Sprintf("\n\n%d IDs are still damaged (see the activity log).", len(left))
	default:
		summary += "\n\nVerified: no damaged partition IDs left."
	}
	a.ui(func() { dialog.ShowInformation("Repair partition IDs", summary, a.win) })
	a.refreshGames()
}
