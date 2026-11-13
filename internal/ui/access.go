package ui

import (
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/freshness79/ps2hdl-manager/internal/system"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------- UI

// updateAccess refreshes the access chip next to the disk picker.
func (a *App) updateAccess() {
	dev := a.cfg.Device
	if dev == "" || a.accessBtn == nil {
		return
	}
	acc := system.CheckAccess(dev)
	a.ui(func() {
		a.accessBtn.SetText(acc.Summary())
		switch {
		case acc.Read && acc.Write:
			a.accessBtn.Importance = widget.SuccessImportance
			a.accessBtn.SetIcon(theme.ConfirmIcon())
		case acc.Read:
			a.accessBtn.Importance = widget.WarningImportance
			a.accessBtn.SetIcon(theme.WarningIcon())
		default:
			a.accessBtn.Importance = widget.DangerImportance
			a.accessBtn.SetIcon(theme.ErrorIcon())
		}
		a.accessBtn.Show()
		a.accessBtn.Refresh()
		if a.headerBar != nil {
			a.headerBar.Refresh() // re-layout: the indicator changes width
		}
	})
	if !acc.Write && !acc.Missing {
		a.log("WARNING disk access: " + acc.Summary() + " — click the access indicator next to the disk for how to fix it")
	}
}

// showAccessReport checks the selected disk and explains the result.
func (a *App) showAccessReport() {
	dev := a.cfg.Device
	if dev == "" {
		dialog.ShowInformation("Disk access", "Select a disk first.", a.win)
		return
	}
	go func() {
		acc := system.CheckAccess(dev)
		a.updateAccess()
		a.ui(func() { a.accessDialog(acc, "") })
	}()
}

func (a *App) accessDialog(acc system.Access, blocked string) {
	mark := func(ok bool) string {
		if ok {
			return "✓ yes"
		}
		return "✗ no"
	}
	errText := func(err error) string {
		if err == nil {
			return ""
		}
		return "   (" + err.Error() + ")"
	}
	user := "standard user"
	if acc.Admin {
		user = map[bool]string{true: "Administrator", false: "root"}[runtime.GOOS == "windows"]
	}
	rows := fmt.Sprintf("Disk:     %s\nOpened as: %s\nRunning as: %s\n\nRead:     %s%s\nWrite:    %s%s",
		acc.Device, acc.Path, user, mark(acc.Read), errText(acc.ReadErr), mark(acc.Write), errText(acc.WErr))
	grid := widget.NewTextGridFromString(rows)
	box := container.NewVBox()
	if blocked != "" {
		l := widget.NewLabel(blocked + " needs write access to the disk.")
		l.Importance = widget.DangerImportance
		l.Wrapping = fyne.TextWrapWord
		box.Add(l)
	}
	box.Add(grid)
	if acc.Hint != "" {
		h := widget.NewLabel(acc.Hint)
		h.Wrapping = fyne.TextWrapWord
		box.Add(widget.NewSeparator())
		box.Add(h)
		if strings.Contains(acc.Hint, "    ") {
			cmd := strings.TrimSpace(acc.Hint[strings.Index(acc.Hint, "    "):])
			if i := strings.Index(cmd, "\n"); i > 0 {
				cmd = cmd[:i]
			}
			box.Add(widget.NewButtonWithIcon("Copy command", theme.ContentCopyIcon(), func() {
				a.fa.Clipboard().SetContent(cmd)
			}))
		}
	} else {
		ok := widget.NewLabel("Everything is fine: the app can read and write this disk.")
		ok.Importance = widget.SuccessImportance
		box.Add(ok)
	}
	d := dialog.NewCustom("Disk access", "Close", box, a.win)
	d.Resize(fyne.NewSize(620, 0))
	d.Show()
}

// needWrite verifies write access before an operation that modifies the disk;
// when it is missing the reason and the fix are shown instead of a tool error.
// Safe to call from any goroutine.
func (a *App) needWrite(what string) bool {
	dev := a.cfg.Device
	if dev == "" {
		return true // the operation's own checks report the missing disk
	}
	acc := system.CheckAccess(dev)
	if acc.Write {
		return true
	}
	a.log("ERROR " + what + ": no write access to " + dev + " (" + acc.Summary() + ")")
	a.updateAccess()
	a.ui(func() { a.accessDialog(acc, what) })
	return false
}

// beginWrite = needWrite + begin, for operations that modify the disk.
func (a *App) beginWrite(msg, what string) bool {
	return a.needWrite(what) && a.begin(msg)
}
