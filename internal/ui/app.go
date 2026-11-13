package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	legal "github.com/freshness79/ps2hdl-manager"
	"github.com/freshness79/ps2hdl-manager/internal/apa"
	"github.com/freshness79/ps2hdl-manager/internal/buildinfo"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats/builtin"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/system"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"image/color"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const appTitle = "PS2 HDL Manager"

type App struct {
	fa  fyne.App
	win fyne.Window
	cfg *config.Config
	hdl *tools.HDL
	pfs *tools.PFS
	chd *tools.CHDTool

	details *detailsPanel
	db      *gamedb.TitleDB
	info    *gamedb.InfoStore

	srcMu sync.Mutex      // picture sources, rebuilt when their URL changes
	icons *gamedb.IconDB  // HDD-OSD icons (see iconDB)
	oplm  *gamedb.OPLMArt // OPL Manager art backup (see artArchive)

	busy atomic.Bool

	gamesMu  sync.RWMutex // a.games is replaced on the UI thread and read by workers
	games    []tools.Game
	filtered []tools.Game
	selected int

	devSelect *widget.Select
	filter    *widget.Entry
	table     *widget.Table
	status    *widget.Label
	progress  *usageBar
	subtitle  *canvas.Text
	libCount  *canvas.Text
	libHead   *fyne.Container
	repairBtn *widget.Button // shown when partition IDs need repair
	accessBtn *widget.Button // disk permission indicator
	headerBar *fyne.Container
	toolInfo  *canvas.Text
	statusBar *fyne.Container
	tGames    *statTile
	tSize     *statTile
	tDB       *statTile
	usage     *usageBar
	usageVal  *canvas.Text
	usageSub  *canvas.Text

	logMu     sync.Mutex
	logFile   *os.File // persistent activity log (ps2hdl.log in the config folder)
	crashFile *os.File // fatal panics are written here (crash.log)
	logLines  []string
	logList   *widget.List
}

// Run starts the graphical application (blocks until the window is closed).
func Run() {
	a := &App{cfg: config.Load(), db: gamedb.NewTitleDB(), selected: -1}
	a.logFile, a.crashFile = openLogs()
	system.DetachConsole(a.logFile) // purely graphical: never read from or write to a terminal
	if a.logFile != nil {
		log.SetOutput(a.logFile) // toolkit warnings go to the log file
		tools.TraceOut = a.logFile
	} else {
		log.SetOutput(io.Discard)
	}
	a.info = gamedb.NewInfoStore(a.db)
	a.applySources()
	a.hdl, a.pfs = tools.EmbeddedHDL(), tools.EmbeddedPFS()
	a.chd = &tools.CHDTool{}
	builtin.CHD = a.chd // used by the CHD format plug-in
	a.fa = app.NewWithID("io.github.freshness79.ps2hdl-manager")
	a.fa.SetIcon(appIcon) // window, taskbar and dock icon
	a.fa.Settings().SetTheme(slickTheme{})
	a.win = a.fa.NewWindow(appTitle)
	a.win.Resize(fyne.NewSize(1440, 860))
	a.build()
	a.win.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) { a.openInstaller(uris) })
	a.win.Show()
	go a.startup()
	a.fa.Run()
}

// ---------------------------------------------------------------- UI

func (a *App) build() {
	a.win.SetMainMenu(fyne.NewMainMenu(
		fyne.NewMenu("File",
			fyne.NewMenuItem("Settings…", a.showSettings),
		),
		fyne.NewMenu("Disk",
			fyne.NewMenuItem("Detect disks", func() { go a.detectDisks() }),
			fyne.NewMenuItem("Set device manually…", a.manualDevice),
			fyne.NewMenuItem("Partition table (TOC)", a.showTOC),
			fyne.NewMenuItem("Repair partition IDs…", a.repairPartitionIDs),
			fyne.NewMenuItem("Check disk access…", a.showAccessReport),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Initialize / format PS2 disk…", a.initializeDisk),
		),
		fyne.NewMenu("Games",
			fyne.NewMenuItem("Refresh list", func() { go a.refreshGames() }),
			fyne.NewMenuItem("Install games…", func() { a.openInstaller(nil) }),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Rename selected…", a.renameSelected),
			fyne.NewMenuItem("Game info & compatibility…", a.gameInfoDialog),
			fyne.NewMenuItem("HDL header info (hdl_dump)", a.infoSelected),
			fyne.NewMenuItem("Extract selected to ISO…", a.extractSelected),
			fyne.NewMenuItem("Delete selected…", a.deleteSelected),
		),
		fyne.NewMenu("Database",
			fyne.NewMenuItem("Update online database", func() { go a.updateDB(true) }),
			fyne.NewMenuItem("Resync all titles…", func() { go a.resyncAll() }),
			fyne.NewMenuItem("Apply recommended compatibility modes…", func() { go a.applyCompatAll() }),
			fyne.NewMenuItem("Write OPL game info (all games)…", a.writeInfoAll),
			fyne.NewMenuItem("Look up ID in database…", a.lookupDialog),
		),
		fyne.NewMenu("Artwork",
			fyne.NewMenuItem("Install OPL art for all games", func() { go a.downloadAllArt(false) }),
			fyne.NewMenuItem("Re-download all OPL art (replace)", func() { go a.downloadAllArt(true) }),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Download OPL art for selected", a.downloadSelectedArt),
			fyne.NewMenuItem("Set cover for selected from file…", a.pickSelectedCover),
			fyne.NewMenuItem("Remove OPL art for selected", a.removeSelectedArt),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Write HDD-OSD titles & icons (all games)", func() { a.osdAll() }),
			fyne.NewMenuItem("Write HDD-OSD title & icon for selected", a.osdSelected),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Create OPL partition (+OPL/ART)", a.createOPLPartition),
			fyne.NewMenuItem("List OPL art on disk", a.listArt),
		),
		fyne.NewMenu("Tools",
			fyne.NewMenuItem("Convert CHD to 2048-byte DVD CHD…", a.convertCHDDialog),
			fyne.NewMenuItem("Extract CHD to ISO…", a.extractCHDDialog),
		),
		fyne.NewMenu("Help",
			fyne.NewMenuItem("Built-in tools", a.showTools),
			fyne.NewMenuItem("Log files location", func() {
				a.textDialog("Log files", "Activity log:  "+filepath.Join(config.Dir(), "ps2hdl.log")+
					"\nCrash log:     "+filepath.Join(config.Dir(), "crash.log")+
					"\n\nAttach both when reporting a problem.")
			}),
		),
		fyne.NewMenu("About",
			fyne.NewMenuItem("About "+appTitle+"…", a.about),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("License (GNU GPL)…", func() { a.licenseWindow("GNU General Public License", legal.License) }),
			fyne.NewMenuItem("Third-party licenses…", func() { a.licenseWindow("Third-party software notices", legal.ThirdPartyNotices) }),
		),
	))

	// ---- header: title, subtitle, disk picker, accent line
	title := canvas.NewText("PS2 HDL Manager", colText)
	title.TextSize = 17
	title.TextStyle = fyne.TextStyle{Bold: true}
	a.subtitle = canvas.NewText(appTagline, colMuted)
	a.subtitle.TextSize = 11
	brand := container.NewHBox(appLogo(40),
		container.NewVBox(layout.NewSpacer(), title, a.subtitle, layout.NewSpacer()))

	a.devSelect = widget.NewSelect(nil, func(s string) {
		dev := strings.Fields(s)
		if len(dev) == 0 {
			return
		}
		if a.busy.Load() { // never switch disk under a running operation
			if dev[0] != a.cfg.Device {
				dialog.ShowInformation("Busy", "Please wait for the current operation to finish before changing disk.", a.win)
			}
			return
		}
		if a.cfg.Device != dev[0] {
			a.cfg.Device = dev[0]
			_ = a.cfg.Save()
		}
		go a.refreshGames()
	})
	a.devSelect.PlaceHolder = "No disk selected"
	detect := widget.NewButtonWithIcon("Detect disks", theme.SearchIcon(), func() { go a.detectDisks() })
	diskLbl := canvas.NewText("DISK", colMuted)
	diskLbl.TextSize = 10
	diskLbl.TextStyle = fyne.TextStyle{Bold: true}
	a.accessBtn = widget.NewButtonWithIcon("", theme.ConfirmIcon(), a.showAccessReport)
	a.accessBtn.Hide()
	diskPicker := container.NewHBox(
		a.accessBtn,
		container.NewCenter(diskLbl),
		container.NewGridWrap(fyne.NewSize(380, 36), a.devSelect),
		widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), a.manualDevice),
		detect)
	a.headerBar = container.NewBorder(nil, nil, brand, container.NewVBox(layout.NewSpacer(), diskPicker, layout.NewSpacer()))
	header := container.NewVBox(a.headerBar, hairline())

	// ---- stat tiles
	a.tGames = newStatTile("GAMES INSTALLED", theme.GridIcon())
	a.tSize = newStatTile("GAME DATA", theme.FolderIcon())
	a.tDB = newStatTile("TITLE DATABASE", theme.StorageIcon())
	usageCap := canvas.NewText("DISK USAGE", colMuted)
	usageCap.TextSize = 10
	usageCap.TextStyle = fyne.TextStyle{Bold: true}
	a.usageVal = canvas.NewText("—", colText)
	a.usageVal.TextSize = 22
	a.usageVal.TextStyle = fyne.TextStyle{Bold: true}
	a.usageSub = canvas.NewText("", colMuted)
	a.usageSub.TextSize = 11
	a.usage = newUsageBar()
	a.usage.warn = true
	usageTile := card(container.NewVBox(usageCap, a.usageVal, a.usageSub, a.usage.obj))
	stats := container.NewGridWithColumns(4, a.tGames.obj, a.tSize.obj, usageTile, a.tDB.obj)

	// ---- sidebar navigation (darker panel, hover items)
	nav := func(label string, icon fyne.Resource, f func()) fyne.CanvasObject {
		return newNavItem(label, icon, nil, f)
	}
	sidebar := navPanel(container.NewVScroll(container.NewVBox(
		navSection("GAMES"),
		newNavItem("Install games", theme.ContentAddIcon(), colAccent, func() { a.openInstaller(nil) }),
		nav("Refresh list", theme.ViewRefreshIcon(), func() { go a.refreshGames() }),
		nav("Rename", theme.DocumentCreateIcon(), a.renameSelected),
		nav("Game info", theme.InfoIcon(), a.gameInfoDialog),
		nav("Extract to ISO", theme.DownloadIcon(), a.extractSelected),
		nav("Delete", theme.DeleteIcon(), a.deleteSelected),
		navSection("DATABASE"),
		nav("Resync titles", theme.MediaReplayIcon(), func() { go a.resyncAll() }),
		nav("Apply compat modes", theme.ConfirmIcon(), func() { go a.applyCompatAll() }),
		nav("Write OPL info", theme.DocumentIcon(), a.writeInfoAll),
		nav("Update database", theme.DownloadIcon(), func() { go a.updateDB(true) }),
		navSection("ARTWORK"),
		nav("Install OPL art", theme.FileImageIcon(), func() { go a.downloadAllArt(false) }),
		nav("HDD-OSD icons", theme.ComputerIcon(), func() { a.osdAll() }),
		navSection("DISK"),
		nav("Partition table", theme.ListIcon(), a.showTOC),
		nav("Settings", theme.SettingsIcon(), a.showSettings),
	)))

	// ---- game library
	a.filter = widget.NewEntry()
	a.filter.SetPlaceHolder("Search by name, ID or category…")
	a.filter.ActionItem = widget.NewIcon(theme.SearchIcon())
	a.filter.OnChanged = func(string) { a.applyFilter() }

	headers := []string{"TYPE", "NAME", "CATEGORY", "MULTIPLAYER", "STARTUP ID", "SIZE", "MODES", "DMA"}
	a.table = widget.NewTableWithHeaders(
		func() (int, int) { return len(a.filtered), len(headers) },
		func() fyne.CanvasObject {
			badge := canvas.NewRectangle(colAccentLo)
			badge.CornerRadius = 4
			txt := canvas.NewText("DVD", colAccent)
			txt.TextSize = 10
			txt.TextStyle = fyne.TextStyle{Bold: true}
			txt.Alignment = fyne.TextAlignCenter
			pill := container.NewCenter(container.NewStack(badge, container.New(layout.NewCustomPaddedLayout(2, 2, 8, 8), txt)))
			lbl := widget.NewLabel("template text")
			lbl.Truncation = fyne.TextTruncateEllipsis
			rowBG := canvas.NewRectangle(color.Transparent)
			return container.NewStack(rowBG, lbl, pill)
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			st := o.(*fyne.Container)
			rowBG := st.Objects[0].(*canvas.Rectangle)
			lbl := st.Objects[1].(*widget.Label)
			pill := st.Objects[2].(*fyne.Container)
			if id.Row == a.selected {
				rowBG.FillColor = colRowSel
			} else {
				rowBG.FillColor = color.Transparent
			}
			rowBG.Refresh()
			if id.Row >= len(a.filtered) {
				lbl.SetText("")
				pill.Hide()
				return
			}
			g := a.filtered[id.Row]
			gi := a.info.Cached(g.Startup)
			setPill := func(text string, c, fg color.Color) {
				lbl.SetText("")
				ps := pill.Objects[0].(*fyne.Container)
				bg := ps.Objects[0].(*canvas.Rectangle)
				txt := ps.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
				txt.Text = text
				txt.Color = fg
				bg.FillColor = c
				bg.Refresh()
				txt.Refresh()
				pill.Show()
			}
			pill.Hide()
			lbl.TextStyle = fyne.TextStyle{}
			lbl.Importance = widget.MediumImportance
			switch id.Col {
			case 0:
				if g.Type == "CD" {
					setPill("CD", colNeutral, colMuted)
				} else {
					setPill("DVD", colAccentLo, colAccent)
				}
				return
			case 1:
				lbl.TextStyle = fyne.TextStyle{Bold: true}
				lbl.SetText(g.Name)
				if g.Hidden {
					lbl.Importance = widget.LowImportance
				}
			case 2:
				if gi == nil {
					lbl.Importance = widget.LowImportance
					lbl.SetText("…")
				} else {
					lbl.SetText(dash(gi.Genre))
				}
			case 3:
				switch {
				case gi == nil:
					lbl.Importance = widget.LowImportance
					lbl.SetText("…")
				case gi.Players > 1:
					setPill(gi.MultiplayerText(), colGreenLo, colGreen)
					return
				default:
					lbl.Importance = widget.LowImportance
					lbl.SetText(gi.MultiplayerText())
				}
			case 4:
				lbl.TextStyle = fyne.TextStyle{Monospace: true}
				lbl.SetText(g.Startup)
			case 5:
				lbl.SetText(fmt.Sprintf("%.2f GB", float64(g.SizeKB)/1024/1024))
			case 6:
				cur := gamedb.FlagsToMask(g.Flags)
				switch {
				case gi != nil && gi.ModesMask != 0 && cur&gi.ModesMask != gi.ModesMask:
					lbl.Importance = widget.WarningImportance
					lbl.SetText("needs " + strings.TrimPrefix(gamedb.FlagsString(gi.ModesMask), "+"))
				case g.Flags != "":
					lbl.SetText(g.Flags)
				default:
					lbl.Importance = widget.LowImportance
					lbl.SetText("—")
				}
			case 7:
				lbl.SetText(g.DMA)
			}
		})
	a.table.ShowHeaderColumn = false
	a.table.CreateHeader = func() fyne.CanvasObject {
		t := canvas.NewText("", colMuted)
		t.TextSize = 10
		t.TextStyle = fyne.TextStyle{Bold: true}
		return container.NewPadded(t)
	}
	a.table.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		if id.Col >= 0 && id.Col < len(headers) {
			t := o.(*fyne.Container).Objects[0].(*canvas.Text)
			t.Text = headers[id.Col]
			t.Refresh()
		}
	}
	for i, w := range []float32{60, 270, 130, 100, 110, 80, 100, 50} {
		a.table.SetColumnWidth(i, w)
	}
	// whole-row selection: the row is painted by the cell template, the table's own
	// single-cell highlight is cleared right away
	a.table.OnSelected = func(id widget.TableCellID) {
		a.selected = id.Row
		a.table.Unselect(id)
		a.table.Refresh()
		if id.Row >= 0 && id.Row < len(a.filtered) {
			g := a.filtered[id.Row]
			a.showDetails(&g)
		}
	}

	libTitle := canvas.NewText("Game library", colText)
	libTitle.TextSize = 15
	libTitle.TextStyle = fyne.TextStyle{Bold: true}
	a.libCount = canvas.NewText("", colMuted)
	a.libCount.TextSize = 12
	a.repairBtn = widget.NewButtonWithIcon("", theme.WarningIcon(), a.repairPartitionIDs)
	a.repairBtn.Importance = widget.WarningImportance
	a.repairBtn.Hide()
	a.libHead = container.NewBorder(nil, nil,
		container.NewHBox(container.NewCenter(libTitle), container.NewCenter(a.libCount), container.NewCenter(a.repairBtn)),
		container.NewGridWrap(fyne.NewSize(320, 36), a.filter))
	library := card(container.NewBorder(container.NewVBox(a.libHead, hairline()), nil, nil, nil, a.table))

	// ---- activity log
	a.logList = widget.NewList(
		func() int { a.logMu.Lock(); defer a.logMu.Unlock(); return len(a.logLines) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle = fyne.TextStyle{Monospace: true}
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			a.logMu.Lock()
			defer a.logMu.Unlock()
			if i < len(a.logLines) {
				l := o.(*widget.Label)
				line := a.logLines[i]
				switch {
				case strings.Contains(line, "ERROR"):
					l.Importance = widget.DangerImportance
				case strings.Contains(line, "WARNING"):
					l.Importance = widget.WarningImportance
				default:
					l.Importance = widget.LowImportance
				}
				l.SetText(line)
			}
		})
	a.logList.OnSelected = func(id widget.ListItemID) {
		a.logList.Unselect(id)
		a.logMu.Lock()
		line := ""
		if id >= 0 && id < len(a.logLines) {
			line = a.logLines[id]
		}
		a.logMu.Unlock()
		if line == "" {
			return
		}
		a.fa.Clipboard().SetContent(line)
		a.status.SetText("Copied to clipboard: " + truncate(line, 80))
	}
	a.status = widget.NewLabel("Ready")
	a.progress = newUsageBar()
	logTitle := canvas.NewText("Activity", colText)
	logTitle.TextSize = 13
	logTitle.TextStyle = fyne.TextStyle{Bold: true}
	logHint := canvas.NewText("click a line to copy it", colFaint)
	logHint.TextSize = 11
	clearLog := widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), func() {
		a.logMu.Lock()
		a.logLines = nil
		a.logMu.Unlock()
		a.logList.Refresh()
	})
	clearLog.Importance = widget.LowImportance
	logCard := card(container.NewBorder(
		container.NewVBox(container.NewBorder(nil, nil, container.NewHBox(container.NewCenter(logTitle), container.NewCenter(logHint)), clearLog), hairline()),
		nil, nil, nil, a.logList))

	// status bar
	a.toolInfo = canvas.NewText("", colFaint)
	a.toolInfo.TextSize = 11
	statusIcon := widget.NewIcon(theme.NewColoredResource(theme.InfoIcon(), theme.ColorNamePlaceHolder))
	a.statusBar = container.NewBorder(hairline(), nil,
		container.NewHBox(statusIcon, a.status),
		container.NewHBox(container.New(fixedWidth{w: 220}, container.NewCenter(a.progress.obj)), container.NewCenter(a.toolInfo)))

	split := container.NewVSplit(library, logCard)
	split.Offset = 0.66

	body := container.NewBorder(nil, nil, container.New(fixedWidth{w: 220}, sidebar), container.New(fixedWidth{w: 260}, a.buildDetails()), split)
	bg := canvas.NewRectangle(colBG)
	a.progress.obj.Hide()
	a.win.SetContent(container.NewStack(bg, container.NewBorder(nil, container.New(layout.NewCustomPaddedLayout(0, 2, 8, 12), a.statusBar), nil, nil, container.NewPadded(
		container.NewBorder(container.NewVBox(header, container.NewPadded(stats)), nil, nil, nil, body)))))
	a.updateToolInfo()
}

// ---------------------------------------------------------------- helpers

func (a *App) ui(f func()) { fyne.Do(f) }

func (a *App) log(line string) {
	line = strings.TrimRight(line, " \t")
	if line == "" {
		return
	}
	a.logMu.Lock()
	a.logLines = append(a.logLines, time.Now().Format("15:04:05 ")+line)
	if a.logFile != nil {
		fmt.Fprintf(a.logFile, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
	}
	if len(a.logLines) > 3000 {
		a.logLines = a.logLines[len(a.logLines)-3000:]
	}
	n := len(a.logLines)
	a.logMu.Unlock()
	a.ui(func() { a.logList.Refresh(); a.logList.ScrollTo(n - 1) })
}

// hdlLine: progress lines move the bar, everything else goes to the log.
func (a *App) hdlLine(line string) {
	if p, ok := tools.ParsePercent(line); ok {
		a.ui(func() { a.progress.SetValue(p) })
		return
	}
	a.log(line)
}

func (a *App) setStatus(s string) { a.ui(func() { a.status.SetText(s) }) }

func (a *App) showErr(err error) {
	a.log("ERROR: " + err.Error())
	a.ui(func() { dialog.ShowError(err, a.win) })
}

// begin takes the operation lock; false if another operation is running.
func (a *App) begin(msg string) bool {
	if !a.busy.CompareAndSwap(false, true) {
		a.ui(func() { dialog.ShowInformation("Busy", "Please wait for the current operation to finish.", a.win) })
		return false
	}
	a.setStatus(msg)
	a.ui(func() { a.progress.SetValue(0); a.progress.obj.Show() })
	return true
}

func (a *App) end(msg string) {
	a.busy.Store(false)
	a.setStatus(msg)
	a.ui(func() { a.progress.obj.Hide() })
}

func (a *App) needReady() bool {
	if a.cfg.Device == "" {
		dialog.ShowInformation("Disk", "Select a PS2 disk first (Disk → Detect disks or Set device manually).", a.win)
		return false
	}
	return true
}

// needCmd checks that the hdl_dump build provides cmd.
func (a *App) needCmd(cmd, what string) bool {
	if a.hdl.Supports(cmd) {
		return true
	}
	ver := a.hdl.Version
	if ver == "" {
		ver = "this build"
	}
	dialog.ShowInformation("Not supported",
		fmt.Sprintf("%s is not available: hdl_dump %s has no '%s' command.", what, ver, cmd), a.win)
	return false
}

func (a *App) selectedGame() (tools.Game, bool) {
	if a.selected < 0 || a.selected >= len(a.filtered) {
		dialog.ShowInformation("Selection", "Select a game in the table first.", a.win)
		return tools.Game{}, false
	}
	return a.filtered[a.selected], true
}

// ---------------------------------------------------------------- startup

func (a *App) startup() {
	if err := a.db.LoadCache(); err == nil {
		a.updateDBLabel()
	}
	if a.db.Count() == 0 || time.Since(a.db.Updated) > 30*24*time.Hour {
		go a.updateDB(false)
	}
	a.probe()
	a.detectDisks()
}

func (a *App) probe() {
	a.hdl.Probe(context.Background())
	v := a.hdl.Version
	if v == "" {
		v = "unknown version"
	}
	a.log(fmt.Sprintf("Built-in tools: hdl_dump %s, pfsshell, CHD codec (v3–v5)", v))
	a.ui(a.updateToolInfo)
}

// ---------------------------------------------------------------- disks

func (a *App) detectDisks() {
	if !a.begin("Detecting disks…") {
		return
	}
	disks, err := a.hdl.Query(context.Background(), a.log)
	a.end("Ready")
	if err != nil {
		a.showErr(err)
		return
	}
	opts := make([]string, 0, len(disks))
	sel := ""
	for _, d := range disks {
		s := d.Device + "  " + d.Info
		opts = append(opts, s)
		a.log("  " + s)
		if d.Device == a.cfg.Device || (sel == "" && strings.Contains(strings.ToLower(d.Info), "playstation")) {
			sel = s
		}
	}
	if a.cfg.Device != "" && sel == "" {
		s := a.cfg.Device + "  (manual)"
		opts = append(opts, s)
		sel = s
	}
	a.log(fmt.Sprintf("Found %d disks", len(disks)))
	a.ui(func() {
		a.devSelect.Options = opts
		a.devSelect.Refresh()
		if sel != "" {
			a.devSelect.SetSelected(sel)
		}
	})
}

// normalizeDevice fixes common mistakes (macOS raw devices are rejected by hdl_dump).
func normalizeDevice(dev string) string {
	dev = strings.TrimSpace(dev)
	if strings.HasPrefix(dev, "/dev/rdisk") {
		return "/dev/disk" + strings.TrimPrefix(dev, "/dev/rdisk")
	}
	return dev
}

func (a *App) manualDevice() {
	e := widget.NewEntry()
	e.SetText(a.cfg.Device)
	e.SetPlaceHolder("e.g. hdd1: (Windows)  /dev/disk4 (macOS)  /dev/sdb (Linux)")
	dialog.ShowForm("Manual device", "OK", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Device", e)}, func(ok bool) {
			if !ok || strings.TrimSpace(e.Text) == "" {
				return
			}
			dev := normalizeDevice(e.Text)
			if dev != strings.TrimSpace(e.Text) {
				a.log("Using " + dev + " (hdl_dump requires the block device, not /dev/rdisk)")
			}
			s := dev + "  (manual)"
			a.devSelect.Options = append(a.devSelect.Options, s)
			a.devSelect.SetSelected(s)
		}, a.win)
}

func (a *App) showTOC() {
	if !a.needReady() {
		return
	}
	go func() {
		if !a.begin("Reading TOC…") {
			return
		}
		out, err := a.hdl.TOC(context.Background(), a.cfg.Device, nil)
		a.end("Ready")
		if err != nil {
			a.showErr(err)
			return
		}
		a.ui(func() { a.textDialog("Partition table "+a.cfg.Device, out) })
	}()
}

func (a *App) textDialog(title, text string) {
	g := widget.NewTextGridFromString(text)
	s := container.NewScroll(g)
	s.SetMinSize(fyne.NewSize(820, 460))
	dialog.ShowCustom(title, "Close", s, a.win)
}

func (a *App) initializeDisk() {
	if !a.needReady() {
		return
	}
	usePFS := !a.hdl.Supports("initialize")
	dev := a.cfg.Device
	if m := system.MountedVolumes(dev); len(m) > 0 {
		dialog.ShowError(fmt.Errorf("%s has mounted volumes (%s).\nIt is in use by the computer: refusing to format it.\nEject/unmount it first if you are sure it is the right disk", dev, strings.Join(m, ", ")), a.win)
		return
	}
	desc := a.devSelect.Selected
	if desc == "" {
		desc = dev
	}
	confirm := widget.NewEntry()
	confirm.SetPlaceHolder("type FORMAT")
	warn := widget.NewLabel(fmt.Sprintf("ALL data on this disk will be ERASED and it will be formatted for PS2:\n\n    %s", desc))
	warn.Importance = widget.DangerImportance
	content := container.NewVBox(warn)
	if note := system.SystemDiskNote(dev); note != "" {
		n := widget.NewLabel(note)
		n.Importance = widget.WarningImportance
		content.Add(n)
	}
	content.Add(widget.NewLabel("Type FORMAT to confirm:"))
	content.Add(confirm)
	dialog.ShowCustomConfirm("Initialize disk", "Format", "Cancel", content, func(ok bool) {
		if !ok || confirm.Text != "FORMAT" {
			return
		}
		go func() {
			if !a.beginWrite("Formatting "+dev+"…", "Formatting the disk") {
				return
			}
			var err error
			if usePFS {
				err = a.pfs.Initialize(context.Background(), dev, a.log)
			} else {
				err = a.hdl.Initialize(context.Background(), dev, a.hdlLine)
			}
			a.end("Ready")
			if err != nil {
				a.showErr(err)
				return
			}
			a.log("Disk initialized.")
			a.refreshGames()
		}()
	}, a.win)
}

// ---------------------------------------------------------------- games

func (a *App) refreshGames() {
	if a.cfg.Device == "" {
		return
	}
	if !a.begin("Reading game list…") {
		return
	}
	a.updateAccess()
	games, usage, err := a.hdl.List(context.Background(), a.cfg.Device, nil)
	if err != nil {
		a.end("Error")
		a.showErr(err)
		return
	}
	sort.Slice(games, func(i, j int) bool { return strings.ToLower(games[i].Name) < strings.ToLower(games[j].Name) })
	dirty := 0
	for _, g := range games {
		if g.DirtyID {
			dirty++
			a.log("WARNING " + g.Partition + ": partition ID has leftover bytes after the name (the PS2/pfsshell may not find it)")
		}
	}
	if parts, err := tools.DirtyPartitions(a.cfg.Device); err == nil {
		dirty = len(parts) // also non-game partitions (e.g. +OPL)
	}
	if dirty > 0 {
		a.log(fmt.Sprintf("WARNING %d damaged partition IDs: click “Repair %d IDs” above the game list (or Disk → Repair partition IDs)", dirty, dirty))
	}
	a.updateRepairButton(dirty)
	var tot int64
	cd, dvd := 0, 0
	for _, g := range games {
		tot += g.SizeKB
		if g.Type == "CD" {
			cd++
		} else {
			dvd++
		}
	}
	a.end(fmt.Sprintf("%d games on %s", len(games), a.cfg.Device))
	a.ui(func() {
		a.gamesMu.Lock()
		a.games = games
		a.gamesMu.Unlock()
		a.tGames.Set(fmt.Sprintf("%d", len(games)), fmt.Sprintf("%d DVD · %d CD", dvd, cd))
		a.tSize.Set(fmt.Sprintf("%.1f GB", float64(tot)/1024/1024), "on "+a.cfg.Device)
		if usage != nil && usage.Total > 0 {
			f := float64(usage.Used) / float64(usage.Total)
			a.usageVal.Text = fmt.Sprintf("%.0f%%", f*100)
			a.usageSub.Text = fmt.Sprintf("%.1f / %.1f GB · %.1f GB free", float64(usage.Used)/1024, float64(usage.Total)/1024, float64(usage.Free)/1024)
			a.usage.Set(f)
		} else {
			a.usageVal.Text = "—"
			a.usageSub.Text = "not reported"
			a.usage.Set(0)
		}
		a.usageVal.Refresh()
		a.usageSub.Refresh()
		a.applyFilter()
	})
	go a.fetchInfos(games)
}

func (a *App) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(a.filter.Text))
	a.filtered = a.filtered[:0]
	for _, g := range a.games {
		genre := ""
		if gi := a.info.Cached(g.Startup); gi != nil {
			genre = strings.ToLower(gi.Genre)
		}
		if q == "" || strings.Contains(strings.ToLower(g.Name), q) || strings.Contains(strings.ToLower(g.Startup), q) || strings.Contains(genre, q) {
			a.filtered = append(a.filtered, g)
		}
	}
	a.selected = -1
	if a.details != nil {
		a.showDetails(nil)
	}
	if a.libCount != nil {
		if len(a.filtered) == len(a.games) {
			a.libCount.Text = fmt.Sprintf("  %d titles", len(a.games))
		} else {
			a.libCount.Text = fmt.Sprintf("  %d of %d titles", len(a.filtered), len(a.games))
		}
		a.libCount.Refresh()
		if a.libHead != nil {
			a.libHead.Refresh()
		}
	}
	a.table.UnselectAll()
	a.table.Refresh()
}

func (a *App) renameSelected() {
	if !a.needReady() || !a.needCmd("modify", "Rename") {
		return
	}
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	e := widget.NewEntry()
	e.SetText(g.Name)
	sug := widget.NewButtonWithIcon("Title from DB", theme.SearchIcon(), func() {
		if t, ok := a.db.Lookup(g.Startup); ok {
			e.SetText(gamedb.CleanTitle(t))
		} else {
			dialog.ShowInformation("Database", "ID "+g.Startup+" not found in the database.", a.win)
		}
	})
	dialog.ShowForm("Rename game", "Rename", "Cancel", []*widget.FormItem{
		widget.NewFormItem("ID", widget.NewLabel(g.Startup)),
		widget.NewFormItem("New name", e),
		widget.NewFormItem("", sug),
	}, func(ok bool) {
		nn := gamedb.CleanTitle(e.Text)
		if !ok || nn == "" || nn == g.Name {
			return
		}
		go func() {
			if !a.beginWrite("Renaming…", "Renaming a game") {
				return
			}
			var err error
			if a.hdl.CanBatch(g) { // one process, no lookup of the game by name
				ops := []tools.BatchOp{{Game: g, NewName: nn}}
				if err = a.hdl.Batch(context.Background(), a.cfg.Device, ops, nil); err == nil {
					err = ops[0].Err
				}
				if err == nil {
					r := g
					r.Name = nn
					a.osdAfterRename([]tools.Game{r})
				}
			} else {
				err = a.hdl.Rename(context.Background(), a.cfg.Device, g.Name, nn, a.hdlLine)
			}
			a.end("Ready")
			if err != nil {
				a.showErr(err)
				return
			}
			a.log(fmt.Sprintf("Renamed: %q → %q", g.Name, nn))
			a.refreshGames()
		}()
	}, a.win)
}

func (a *App) infoSelected() {
	if !a.needReady() || !a.needCmd("info", "Game info") {
		return
	}
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	go func() {
		if !a.begin("Reading info…") {
			return
		}
		out, err := a.hdl.Info(context.Background(), a.cfg.Device, g.Name, nil)
		a.end("Ready")
		if err != nil {
			a.showErr(err)
			return
		}
		if t, ok := a.db.Lookup(g.Startup); ok {
			out += "\nTitle in online DB: " + t
		}
		a.ui(func() { a.textDialog(g.Name, out) })
	}()
}

func (a *App) extractSelected() {
	if !a.needReady() || !a.needCmd("extract", "Extract") {
		return
	}
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			return
		}
		out := w.URI().Path()
		w.Close()
		_ = os.Remove(out) // hdl_dump creates the file itself
		go func() {
			if !a.begin("Extracting " + g.Name + "…") {
				return
			}
			err := a.hdl.Extract(context.Background(), a.cfg.Device, g.Name, out, a.hdlLine)
			a.end("Ready")
			if err != nil {
				a.showErr(err)
				return
			}
			a.log("Extracted to " + out)
		}()
	}, a.win)
	d.SetFileName(g.Startup + "." + gamedb.CleanTitle(g.Name) + ".iso")
	d.Resize(fyne.NewSize(800, 550))
	d.Show()
}

func (a *App) deleteSelected() {
	if !a.needReady() {
		return
	}
	useHDL := a.hdl.Supports("delete")
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	alsoArt := widget.NewCheck("Also remove its cover art and OPL CFG", nil)
	alsoArt.SetChecked(true)
	content := container.NewVBox(widget.NewLabel(fmt.Sprintf("Permanently delete\n%q (%s)?", g.Name, g.Startup)), alsoArt)
	dialog.ShowCustomConfirm("Delete game", "Delete", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		if useHDL {
			go a.doDelete(g, "", alsoArt.Checked)
			return
		}
		if g.Partition != "" { // exact partition read from the disk: no guessing needed
			go a.doDelete(g, g.Partition, alsoArt.Checked)
			return
		}
		go func() {
			parts, err := a.hdl.HDLPartitions(context.Background(), a.cfg.Device)
			if err != nil {
				a.showErr(err)
				return
			}
			part, cands := tools.FindGamePartition(g, parts)
			if part != "" {
				a.doDelete(g, part, alsoArt.Checked)
				return
			}
			// not certain: let the user pick the partition
			if len(cands) == 0 {
				cands = parts
			}
			a.log(fmt.Sprintf("Partition of %q not identified with certainty (expected %s); asking.", g.Name, tools.HDLPartName(g.Startup, g.Name, "PP.")))
			a.ui(func() { a.choosePartition(g, cands, alsoArt.Checked) })
		}()
	}, a.win)
}

func (a *App) choosePartition(g tools.Game, cands []string, alsoArt bool) {
	if len(cands) == 0 {
		dialog.ShowError(fmt.Errorf("no HDL game partitions found on %s", a.cfg.Device), a.win)
		return
	}
	sel := widget.NewRadioGroup(cands, nil)
	sc := container.NewVScroll(sel)
	sc.SetMinSize(fyne.NewSize(520, 260))
	content := container.NewBorder(widget.NewLabel(fmt.Sprintf("Which partition belongs to %q (%s)?", g.Name, g.Startup)), nil, nil, nil, sc)
	dialog.ShowCustomConfirm("Choose partition", "Delete", "Cancel", content, func(ok bool) {
		if ok && sel.Selected != "" {
			go a.doDelete(g, sel.Selected, alsoArt)
		}
	}, a.win)
}

// doDelete removes a game and verifies it. With pfsshell everything (partition,
// cover art, OPL CFG) is done in a single session; the check reads the disk directly.
func (a *App) doDelete(g tools.Game, part string, alsoArt bool) {
	if !a.beginWrite("Deleting "+g.Name+"…", "Deleting a game") {
		return
	}
	ctx := context.Background()
	var err error
	if part == "" {
		err = a.hdl.Delete(ctx, a.cfg.Device, g.Name, a.hdlLine)
	} else {
		cmds := []string{"rmpart " + tools.Quote(part)}
		if alsoArt && a.otherCopies(g) == 0 {
			if ok, _ := a.pfs.PartitionExists(ctx, a.cfg.Device, a.cfg.OPLPart); ok {
				st := disc.StartupFromSerial(disc.NormalizeSerial(g.Startup))
				cmds = append(cmds,
					"mount "+tools.Quote(a.cfg.OPLPart),
					"cd /"+tools.ArtDir(a.cfg.OPLPart))
				for _, f := range gamedb.AllArtNames(g.Startup) {
					cmds = append(cmds, "rm "+tools.Quote(f))
				}
				cmds = append(cmds,
					"cd /"+tools.CfgDir(a.cfg.OPLPart), "rm "+st+".cfg",
					"umount")
			}
		}
		if g.DirtyID && g.Partition == part && a.hdl.CanBatch(g) {
			// id left dirty by an old rename: pfsshell would not find the partition
			a.log("Repairing partition id of " + part)
			ops := []tools.BatchOp{{Game: g, CleanID: true}}
			if e := a.hdl.Batch(ctx, a.cfg.Device, ops, nil); e != nil || ops[0].Err != nil {
				a.log(fmt.Sprintf("Partition id repair failed: %v %v", e, ops[0].Err))
			}
		}
		a.log("Removing partition " + part)
		_, err = a.pfs.Run(ctx, a.cfg.Device, "", cmds, a.log, tools.TolMissing...)
		if err == nil {
			if d, e := apa.Scan(a.cfg.Device); e == nil && d.HasPartition(part) {
				err = fmt.Errorf("partition %s is still present after rmpart", part)
			}
		}
	}
	if part == "" && err == nil && alsoArt && a.otherCopies(g) == 0 {
		_ = a.pfs.RmArt(ctx, a.cfg.Device, a.cfg.OPLPart, gamedb.AllArtNames(g.Startup), nil)
	}
	a.end("Ready")
	if err != nil {
		a.showErr(err)
		return
	}
	if alsoArt && a.otherCopies(g) == 0 {
		for _, t := range gamedb.ArtTypes {
			if cp := gamedb.CachedArt(g.Startup, t.Key); cp != "" {
				_ = os.Remove(cp)
			}
		}
	}
	a.log("Deleted: " + g.Name)
	a.ui(func() { a.showDetails(nil) })
	a.refreshGames()
}

// otherCopies counts other installed games with the same startup ID
// (their cover/CFG must be kept).
func (a *App) otherCopies(g tools.Game) int {
	n := 0
	for _, x := range a.gamesSnapshot() {
		if disc.NormalizeSerial(x.Startup) == disc.NormalizeSerial(g.Startup) && x.Name != g.Name {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- database

func (a *App) updateDBLabel() {
	txt := fmt.Sprintf("Title DB: %d entries", a.db.Count())
	if !a.db.Updated.IsZero() {
		txt += " (updated " + a.db.Updated.Format("2006-01-02") + ")"
	}
	sub := "online"
	if !a.db.Updated.IsZero() {
		sub = "updated " + a.db.Updated.Format("2006-01-02")
	}
	_ = txt
	a.ui(func() { a.tDB.Set(fmt.Sprintf("%d", a.db.Count()), sub) })
}

func (a *App) updateDB(notify bool) {
	a.log("Downloading title database from " + a.cfg.DBURL)
	if err := a.db.Download(a.cfg.DBURL); err != nil {
		a.log("Database download failed: " + err.Error())
		if notify {
			a.showErr(err)
		}
		return
	}
	a.log(fmt.Sprintf("Database updated: %d titles", a.db.Count()))
	a.updateDBLabel()
	if notify {
		a.ui(func() {
			dialog.ShowInformation("Database", fmt.Sprintf("Database updated: %d titles.", a.db.Count()), a.win)
		})
	}
}

func (a *App) lookupDialog() {
	e := widget.NewEntry()
	e.SetPlaceHolder("e.g. SLUS_123.45 or SLES-12345")
	res := widget.NewLabel("")
	res.Wrapping = fyne.TextWrapWord
	e.OnSubmitted = func(s string) {
		if t, ok := a.db.Lookup(s); ok {
			res.SetText(t)
		} else {
			res.SetText("Not found")
		}
	}
	c := container.NewVBox(e, widget.NewButton("Search", func() { e.OnSubmitted(e.Text) }), res)
	d := dialog.NewCustom("Database lookup", "Close", c, a.win)
	d.Resize(fyne.NewSize(480, 220))
	d.Show()
}

type rename struct {
	game  tools.Game
	newNm string
	check *widget.Check
}

// resyncAll compares names on disk with the online DB and proposes renames.
func (a *App) resyncAll() {
	if a.cfg.Device == "" {
		a.ui(func() { a.needReady() })
		return
	}
	if !a.hdl.Supports("modify") {
		a.ui(func() { a.needCmd("modify", "Title resync") })
		return
	}
	if a.db.Count() == 0 {
		a.updateDB(false)
		if a.db.Count() == 0 {
			a.showErr(fmt.Errorf("title database not available"))
			return
		}
	}
	if !a.begin("Comparing titles with the database…") {
		return
	}
	games, _, err := a.hdl.List(context.Background(), a.cfg.Device, nil)
	a.end("Ready")
	if err != nil {
		a.showErr(err)
		return
	}
	var changes []*rename
	missing := 0
	for _, g := range games {
		t, ok := a.db.Lookup(g.Startup)
		if !ok {
			missing++
			continue
		}
		nn := gamedb.CleanTitle(t)
		if nn != "" && nn != g.Name {
			changes = append(changes, &rename{game: g, newNm: nn})
		}
	}
	a.log(fmt.Sprintf("Resync: %d games, %d to rename, %d not in DB", len(games), len(changes), missing))
	if len(changes) == 0 {
		a.ui(func() {
			dialog.ShowInformation("Resync", fmt.Sprintf("All titles are already in sync.\n(%d IDs not found in the DB)", missing), a.win)
		})
		return
	}
	a.ui(func() { a.showResyncDialog(changes, missing) })
}

func (a *App) showResyncDialog(changes []*rename, missing int) {
	rows := container.NewVBox()
	for _, c := range changes {
		c.check = widget.NewCheck("", nil)
		c.check.SetChecked(true)
		old := widget.NewLabel(c.game.Name)
		old.Truncation = fyne.TextTruncateEllipsis
		nw := widget.NewLabelWithStyle(c.newNm, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		nw.Truncation = fyne.TextTruncateEllipsis
		rows.Add(container.NewBorder(nil, nil,
			container.NewHBox(c.check, widget.NewLabel(c.game.Startup)), nil,
			container.NewGridWithColumns(2, old, nw)))
	}
	all := widget.NewCheck("Select all", func(b bool) {
		for _, c := range changes {
			c.check.SetChecked(b)
		}
	})
	all.SetChecked(true)
	scroll := container.NewVScroll(rows)
	scroll.SetMinSize(fyne.NewSize(900, 420))
	head := widget.NewLabel(fmt.Sprintf("%d games differ from the online DB (%d IDs not found). Current name → new name:", len(changes), missing))
	content := container.NewBorder(container.NewVBox(head, all), nil, nil, nil, scroll)
	dialog.ShowCustomConfirm("Resync titles", "Apply", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		var todo []*rename
		for _, c := range changes {
			if c.check.Checked {
				todo = append(todo, c)
			}
		}
		go a.applyRenames(todo)
	}, a.win)
}

func (a *App) applyRenames(todo []*rename) {
	if !a.beginWrite("Renaming…", "Renaming games") {
		return
	}
	okN, errN := 0, 0
	games := make([]tools.Game, len(todo))
	for i, c := range todo {
		games[i] = c.game
	}
	if a.hdl.CanBatch(games...) {
		// all renames in one hdl_dump process, one read of the partition table
		ops := make([]tools.BatchOp, len(todo))
		for i, c := range todo {
			ops[i] = tools.BatchOp{Game: c.game, NewName: c.newNm}
		}
		a.setStatus(fmt.Sprintf("Renaming %d games…", len(todo)))
		err := a.hdl.Batch(context.Background(), a.cfg.Device, ops, func(done int) {
			a.ui(func() { a.progress.SetValue(float64(done) / float64(len(todo))) })
		})
		var renamed []tools.Game
		for i, c := range todo {
			if err == nil && ops[i].Err == nil {
				r := c.game
				r.Name = c.newNm
				renamed = append(renamed, r)
			}
		}
		a.osdAfterRename(renamed)
		if err != nil {
			a.log("ERROR batch rename: " + err.Error())
			errN = len(todo)
		} else {
			for i, c := range todo {
				if ops[i].Err != nil {
					a.log("ERROR " + c.game.Startup + ": " + ops[i].Err.Error())
					errN++
					continue
				}
				a.log(fmt.Sprintf("%s: %q → %q", c.game.Startup, c.game.Name, c.newNm))
				okN++
			}
		}
		todo = nil
	}
	for i, c := range todo {
		a.ui(func() { a.progress.SetValue(float64(i) / float64(len(todo))) })
		a.setStatus(fmt.Sprintf("Renaming %d/%d: %s", i+1, len(todo), c.newNm))
		if err := a.hdl.Rename(context.Background(), a.cfg.Device, c.game.Name, c.newNm, a.log); err != nil {
			a.log("ERROR " + c.game.Startup + ": " + err.Error())
			errN++
			continue
		}
		a.log(fmt.Sprintf("%s: %q → %q", c.game.Startup, c.game.Name, c.newNm))
		okN++
	}
	a.ui(func() { a.progress.SetValue(1) })
	a.end(fmt.Sprintf("Resync complete: %d renamed, %d errors", okN, errN))
	a.refreshGames()
}

// ---------------------------------------------------------------- settings / help

func (a *App) showSettings() {
	if a.busy.Load() {
		dialog.ShowInformation("Busy", "Settings can be changed when the current operation has finished.", a.win)
		return
	}
	hint := func(t string) *widget.Label {
		l := widget.NewLabel(t)
		l.Wrapping = fyne.TextWrapWord
		l.SizeName = theme.SizeNameCaptionText
		return l
	}

	// ---- General
	oplE := widget.NewEntry()
	oplE.SetText(a.cfg.OPLPart)
	dma := widget.NewSelect([]string{"*u4", "*u3", "*u2", "*u1", "*u0", "*m2", "*m1", "*m0"}, nil)
	dma.SetSelected(a.cfg.DMAMode)
	workE := widget.NewEntry()
	workE.SetText(a.cfg.WorkDir)
	workE.SetPlaceHolder(a.workDir() + "  (needs free space ≈ ISO size)")
	general := widget.NewForm(
		widget.NewFormItem("OPL partition", oplE),
		widget.NewFormItem("Default DMA", dma),
		widget.NewFormItem("CHD work folder", workE),
	)

	// ---- After installing
	autoC := widget.NewCheck("Install OPL art automatically", nil)
	autoC.SetChecked(a.cfg.CoversEnabled())
	enabled := map[string]bool{}
	for _, k := range a.artKeys() {
		enabled[k] = true
	}
	var artChecks []*widget.Check
	artGrid := container.NewGridWithColumns(4)
	for _, t := range gamedb.ArtTypes {
		c := widget.NewCheck(t.Label, nil)
		c.SetChecked(enabled[t.Key])
		artChecks = append(artChecks, c)
		artGrid.Add(c)
	}
	autoM := widget.NewCheck("Write the OPL game info sheet and the recommended compatibility modes", nil)
	autoM.SetChecked(a.cfg.CompatEnabled())
	osdC := widget.NewCheck("Write the HDD-OSD (Browser 2.0) title and 3D icon (also when renaming)", nil)
	osdC.SetChecked(a.cfg.OSDEnabled())
	install := widget.NewForm(
		widget.NewFormItem("OPL art", autoC),
		widget.NewFormItem("Picture types", container.NewVBox(artGrid,
			hint("Front cover, disc icon, background and screenshots are shown by OPL's default theme; back cover, spine and logo only by custom themes."))),
		widget.NewFormItem("Game info", autoM),
		widget.NewFormItem("HDD-OSD", osdC),
	)

	// ---- Online sources
	type source struct {
		label, hint, def string
		cur              string
		optional         bool // may be left empty to turn it off
		e                *widget.Entry
	}
	sources := []*source{
		{label: "Game titles", hint: "JSON title database (GameDB-PS2 format)", def: config.DefaultDBURL, cur: a.cfg.DBURL},
		{label: "Front covers", hint: "picture URL per game", def: config.DefaultCoverURL, cur: a.cfg.CoverURL},
		{label: "Other OPL art", hint: "zip archive in OPL Manager layout: icons, backgrounds, screenshots, back covers, spines, logos",
			def: config.DefaultArtArchive, cur: a.cfg.ArtArchiveURL(), optional: true},
		{label: "Info sheets", hint: "OPL CFG per game: players, video mode, description…", def: config.DefaultInfoURL, cur: a.cfg.InfoSource(), optional: true},
		{label: "Compat. list", hint: "list of the games in the compatibility database", def: config.DefaultCompatListURL, cur: a.cfg.CompatListSource(), optional: true},
		{label: "Compat. modes", hint: "OPL CFG per game with $Compatibility", def: config.DefaultCompatCfgURL, cur: a.cfg.CompatCfgSource(), optional: true},
		{label: "HDD-OSD icons", hint: "Icons.ini of an icon pack (icons in ICNS/ beside it)", def: config.DefaultIconIndex, cur: a.cfg.IconIndexURL(), optional: true},
	}
	srcForm := widget.NewForm()
	for _, sr := range sources {
		sr := sr
		sr.e = widget.NewEntry()
		sr.e.SetText(sr.cur)
		if sr.optional {
			sr.e.SetPlaceHolder("empty = off")
		} else {
			sr.e.SetPlaceHolder("empty = default")
		}
		def := widget.NewButton("Default", func() { sr.e.SetText(sr.def) })
		srcForm.AppendItem(&widget.FormItem{Text: sr.label, Widget: container.NewBorder(nil, nil, nil, def, sr.e), HintText: sr.hint})
	}
	srcTab := container.NewVBox(
		hint("In per-game URLs {ID} becomes SLUS-12345 and {STARTUP} SLUS_123.45. Downloaded files are cached: a new source is used for games not loaded yet (Database → Update to reload the titles)."),
		srcForm)

	tabs := container.NewAppTabs(
		container.NewTabItem("General", container.NewPadded(general)),
		container.NewTabItem("After installing", container.NewPadded(install)),
		container.NewTabItem("Online sources", container.NewVScroll(container.NewPadded(srcTab))),
	)
	d := dialog.NewCustomConfirm("Settings", "Save", "Cancel", tabs, func(ok bool) {
		if !ok {
			return
		}
		a.cfg.DMAMode = dma.Selected
		a.cfg.WorkDir = strings.TrimSpace(workE.Text)
		if op := strings.TrimSpace(oplE.Text); op != "" {
			a.cfg.OPLPart = op
		}
		ac, am, ao := autoC.Checked, autoM.Checked, osdC.Checked
		a.cfg.AutoCovers, a.cfg.AutoCompat, a.cfg.AutoOSD = &ac, &am, &ao
		kinds := []string{}
		for i, t := range gamedb.ArtTypes {
			if artChecks[i].Checked {
				kinds = append(kinds, t.Key)
			}
		}
		a.cfg.ArtKinds = kinds
		val := func(i int) string { return strings.TrimSpace(sources[i].e.Text) }
		a.cfg.DBURL = val(0)
		if a.cfg.DBURL == "" {
			a.cfg.DBURL = config.DefaultDBURL
		}
		a.cfg.CoverURL = val(1)
		if a.cfg.CoverURL == "" {
			a.cfg.CoverURL = config.DefaultCoverURL
		}
		a.cfg.ArtArchive = urlSetting(val(2), config.DefaultArtArchive)
		a.cfg.InfoURL = urlSetting(val(3), config.DefaultInfoURL)
		a.cfg.CompatListURL = urlSetting(val(4), config.DefaultCompatListURL)
		a.cfg.CompatCfgURL = urlSetting(val(5), config.DefaultCompatCfgURL)
		a.cfg.IconIndex = urlSetting(val(6), config.DefaultIconIndex)
		a.applySources()
		if err := a.cfg.Save(); err != nil {
			dialog.ShowError(err, a.win)
		}
		go a.probe()
	}, a.win)
	d.Resize(fyne.NewSize(920, 700))
	d.Show()
}

// applySources passes the configured info sources to the info store.
func (a *App) applySources() {
	a.info.SetSources(a.cfg.InfoSource(), a.cfg.CompatListSource(), a.cfg.CompatCfgSource())
}

// urlSetting stores a URL setting: "" for the default, "-" for off.
func urlSetting(text, def string) string {
	switch t := strings.TrimSpace(text); t {
	case def:
		return ""
	case "":
		return "-"
	default:
		return t
	}
}

// showTools describes the built-in tools (nothing external is ever used).
func (a *App) showTools() {
	v := a.hdl.Version
	if v == "" {
		v = "0.9.2"
	}
	txt := "All tools are built into this app: nothing to install or configure.\n\n" +
		"  hdl_dump " + v + "   installing, renaming, compatibility flags, HDD-OSD data, extracting\n" +
		"  pfsshell        OPL partition (art, CFG files), deleting, formatting\n" +
		"  CHD codec       reading CHD v3, v4 and v5 (CD and DVD), writing DVD CHDs\n\n" +
		"Each runs as a child process of this app (the same executable), so a\n" +
		"problem in one never takes down the interface."
	a.textDialog("Built-in tools", txt)
}

// updateToolInfo shows which hdl_dump/pfsshell are in use in the status bar.
func (a *App) updateToolInfo() {
	if a.toolInfo == nil || a.hdl == nil {
		return
	}
	v := "hdl_dump"
	if a.hdl.Version != "" {
		v += " " + a.hdl.Version
	}
	v += " (built-in)"
	a.toolInfo.Text = v + "  ·  v" + buildinfo.Version
	a.toolInfo.Refresh()
	if a.statusBar != nil {
		a.statusBar.Refresh()
	}
}

// gamesSnapshot returns the current game list; safe from any goroutine.
func (a *App) gamesSnapshot() []tools.Game {
	a.gamesMu.RLock()
	defer a.gamesMu.RUnlock()
	return append([]tools.Game(nil), a.games...)
}

// updateRepairButton shows the "Repair N IDs" button when needed.
func (a *App) updateRepairButton(n int) {
	a.ui(func() {
		if a.repairBtn == nil {
			return
		}
		if n == 0 {
			a.repairBtn.Hide()
		} else {
			a.repairBtn.SetText(fmt.Sprintf("Repair %d IDs", n))
			a.repairBtn.Show()
		}
		if a.libHead != nil {
			a.libHead.Refresh()
		}
	})
}

// logProblem writes a headline to the activity log followed by the details that
// explain it (indented, at most 16 lines), so a failing image can be understood
// without opening the diagnostics. level is "ERROR" or "WARNING".
func (a *App) logProblem(level, file, format, headline, details string) {
	where := filepath.Base(file)
	if format != "" {
		where += " [" + format + "]"
	}
	a.log(level + " " + where + ": " + headline)
	n := 0
	for _, l := range strings.Split(details, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "No problems found") {
			continue
		}
		if n == 16 {
			a.log("      … (full details: select the image in the installer and click Info)")
			break
		}
		a.log("      " + l)
		n++
	}
}
