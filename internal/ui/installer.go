package ui

import (
	"context"
	"errors"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/formats"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type queueItem struct {
	Path     string
	format   formats.Format // plug-in handling this file
	info     *formats.Info  // result of format.Probe (nil if probeErr)
	probeErr string
	ID       *widget.Entry
	Title    *widget.Entry
	DVD      *widget.Check
	Status   *widget.Label
	fixBtn   *widget.Button
	done     bool

	titleAuto bool // title not edited by the user: keep it in sync with the DB
	setting   bool
}

// setTitle changes the title programmatically (not counted as a user edit).
func (it *queueItem) setTitle(t string) {
	it.setting = true
	it.Title.SetText(t)
	it.setting = false
}

type installer struct {
	a     *App
	w     fyne.Window
	items []*queueItem
	rows  *fyne.Container
	count *widget.Label
	start *widget.Button
	prog  *usageBar
	run   bool
}

// openInstaller opens (or reuses) the install window and adds the given files.
func (a *App) openInstaller(uris []fyne.URI) *installer {
	in := &installer{a: a}
	in.w = a.fa.NewWindow("Install games to PS2 disk")
	in.w.Resize(fyne.NewSize(1250, 620))
	in.rows = container.NewVBox()
	in.count = widget.NewLabel(emptyQueueText())
	in.prog = newUsageBar()

	addFile := widget.NewButtonWithIcon("Add files…", theme.FileIcon(), in.pickFile)
	addDir := widget.NewButtonWithIcon("Add folder…", theme.FolderOpenIcon(), in.pickFolder)
	clear := widget.NewButtonWithIcon("Clear queue", theme.ContentClearIcon(), func() {
		if in.run {
			return
		}
		in.items = nil
		in.rebuild()
	})
	in.start = widget.NewButtonWithIcon("Install to disk", theme.UploadIcon(), func() { go in.install() })
	in.start.Importance = widget.HighImportance

	clear.Importance = widget.LowImportance

	title := canvas.NewText("Install games", colText)
	title.TextSize = 20
	title.TextStyle = fyne.TextStyle{Bold: true}
	sub := canvas.NewText("Target "+a.cfg.Device+"  ·  titles from the online DB by startup ID (editable)  ·  drop disc images here", colMuted)
	sub.TextSize = 12
	top := container.NewVBox(
		container.NewBorder(nil, nil, container.NewVBox(title, sub), container.NewHBox(addFile, addDir, clear)),
		hairline(),
	)
	bottom := card(container.NewVBox(in.prog.obj, container.NewBorder(nil, nil, nil, in.start, in.count)))
	bg := canvas.NewRectangle(colBG)
	in.w.SetContent(container.NewStack(bg, container.NewPadded(
		container.NewBorder(top, bottom, nil, nil, card(container.NewVScroll(in.rows))))))
	in.w.SetOnDropped(func(_ fyne.Position, u []fyne.URI) { in.addURIs(u) })
	in.w.SetCloseIntercept(func() {
		if in.run {
			dialog.ShowInformation("Install", "Please wait for the installation to finish.", in.w)
			return
		}
		in.w.Close()
	})
	in.w.Show()
	if len(uris) > 0 {
		in.addURIs(uris)
	}
	return in
}

func (in *installer) pickFile() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		u := r.URI()
		r.Close()
		in.a.cfg.LastFolder = filepath.Dir(u.Path())
		_ = in.a.cfg.Save()
		in.addURIs([]fyne.URI{u})
	}, in.w)
	var exts []string
	for _, e := range formats.Extensions() {
		exts = append(exts, e, strings.ToUpper(e))
	}
	d.SetFilter(storage.NewExtensionFileFilter(exts))
	in.setLoc(d)
	d.Show()
}

func (in *installer) pickFolder() {
	d := dialog.NewFolderOpen(func(l fyne.ListableURI, err error) {
		if err != nil || l == nil {
			return
		}
		in.a.cfg.LastFolder = l.Path()
		_ = in.a.cfg.Save()
		in.addURIs([]fyne.URI{l})
	}, in.w)
	in.setLoc(d)
	d.Show()
}

type locSetter interface {
	SetLocation(fyne.ListableURI)
	Resize(fyne.Size)
}

func (in *installer) setLoc(d locSetter) {
	d.Resize(fyne.NewSize(850, 560))
	if in.a.cfg.LastFolder != "" {
		if l, err := storage.ListerForURI(storage.NewFileURI(in.a.cfg.LastFolder)); err == nil {
			d.SetLocation(l)
		}
	}
}

// addURIs adds files or folders (recursively) to the queue.
func (in *installer) addURIs(uris []fyne.URI) {
	var paths []string
	for _, u := range uris {
		p := u.Path()
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			_ = filepath.WalkDir(p, func(fp string, de fs.DirEntry, err error) error {
				if err == nil && !de.IsDir() && formats.Supported(fp) {
					paths = append(paths, fp)
				}
				return nil
			})
		} else if formats.Supported(p) {
			paths = append(paths, p)
		}
	}
	// a multi-file image (CUE+BIN, MDS+MDF, CCD+IMG+SUB) is queued once, through
	// the file that describes it
	seen := map[string]bool{}
	var primaries []string
	for _, p := range paths {
		pp := formats.Primary(p)
		if !seen[pp] {
			seen[pp] = true
			primaries = append(primaries, pp)
		}
	}
	go func() {
		for _, p := range primaries {
			in.addPath(p)
		}
	}()
}

// addPath analyses a file with its format plug-in and queues it.
func (in *installer) addPath(p string) {
	ctx := context.Background()
	p = formats.Primary(p)
	f, err := formats.For(p)
	if err != nil {
		in.a.logProblem("ERROR", p, "", err.Error(), fileFacts(p))
		return
	}
	info, perr := f.Probe(ctx, p)
	it := &queueItem{Path: p, format: f, info: info}
	if perr != nil {
		it.probeErr, info = perr.Error(), &formats.Info{}
		details := fileFacts(p)
		var le *disc.LayoutError
		if errors.As(perr, &le) {
			details = le.Detail() + details
		}
		in.a.logProblem("ERROR", p, f.Name(), "cannot read the image: "+perr.Error(), details)
	} else {
		switch {
		case info.Unsupported != "":
			in.a.logProblem("ERROR", p, f.Name(), info.Unsupported, info.Details)
		case info.Problem != "":
			in.a.logProblem("WARNING", p, f.Name(), info.Problem, info.Details)
		}
	}
	startup := info.Startup
	if startup == "" && perr == nil && !info.IDAtPrepare && formats.IsNative(f) {
		// hdl_dump reads this format itself: ask it (cdvd_info)
		if out, e := in.a.hdl.CDVDInfo(ctx, p); e == nil {
			if m := disc.SerialRe.FindStringSubmatch(strings.ToUpper(out)); m != nil {
				startup = disc.StartupFromSerial(m[1] + m[2] + m[3])
			}
		}
	}
	if startup == "" && !info.IDAtPrepare {
		if m := disc.FileIDRe.FindStringSubmatch(filepath.Base(p)); m != nil { // ID in the file name
			startup = disc.StartupFromSerial(strings.ToUpper(m[1]) + m[2] + m[3])
		}
	}
	title := gamedb.CleanFileTitle(p)
	if t, ok := in.a.db.Lookup(startup); ok {
		title = gamedb.CleanTitle(t)
	}
	dvd := info.Media == formats.DVD
	if info.Media == formats.Unknown {
		if st, err := os.Stat(p); err == nil {
			dvd = st.Size() > 700*1024*1024
		}
	}
	locked := info.Unsupported != ""

	fyne.Do(func() {
		for _, x := range in.items {
			if x.Path == p {
				return
			}
		}
		it.ID = widget.NewEntry()
		it.ID.SetPlaceHolder("SLUS_123.45")
		if info.IDAtPrepare {
			it.ID.SetPlaceHolder("read on install")
		}
		it.ID.SetText(startup)
		it.ID.OnChanged = func(s string) {
			if t, ok := in.a.db.Lookup(s); ok && (it.titleAuto || it.Title.Text == "") {
				it.setTitle(gamedb.CleanTitle(t))
			}
			in.updateStatus(it)
		}
		it.Title = widget.NewEntry()
		it.titleAuto = true
		it.setTitle(title)
		it.Title.OnChanged = func(string) {
			if !it.setting {
				it.titleAuto = false
			}
		}
		it.DVD = widget.NewCheck("DVD", nil)
		it.DVD.SetChecked(dvd)
		if locked {
			it.ID.Disable()
			it.Title.Disable()
			it.DVD.Disable()
		}
		it.Status = widget.NewLabel("")
		it.Status.Truncation = fyne.TextTruncateEllipsis
		in.updateStatus(it)
		in.items = append(in.items, it)
		in.rebuild()
	})
}

// updateStatus summarises the plug-in analysis in the row status label.
func (in *installer) updateStatus(it *queueItem) {
	info := it.info
	id := strings.TrimSpace(it.ID.Text)
	st, imp := "Ready", widget.SuccessImportance
	var notes []string
	if info != nil {
		notes = append(notes, info.Notes...)
	}
	if _, ok := in.a.db.Lookup(id); ok {
		notes = append(notes, "title from DB")
	} else if id != "" {
		notes = append(notes, "not in DB")
	}
	if info != nil && info.NeedsID && id != "" {
		notes = append(notes, "ID passed to hdl_dump")
	}
	for _, g := range in.a.gamesSnapshot() {
		if id != "" && disc.NormalizeSerial(g.Startup) == disc.NormalizeSerial(id) {
			st, imp = "Already on disk: "+g.Name, widget.WarningImportance
		}
	}
	switch {
	case it.probeErr != "":
		st, imp, notes = it.format.Name()+": "+it.probeErr, widget.DangerImportance, nil
	case info.Unsupported != "":
		st, imp, notes = info.Unsupported, widget.DangerImportance, nil
	case info.Problem != "":
		st, imp, notes = info.Problem, widget.DangerImportance, nil
	case info.NeedsID && id == "":
		st, imp, notes = "Enter the startup ID (not readable from the image)", widget.DangerImportance, nil
	case id == "" && !info.IDAtPrepare:
		st, imp = "Startup ID not detected", widget.WarningImportance
	}
	if len(notes) > 0 {
		st += " · " + strings.Join(notes, " · ")
	}
	it.Status.Importance = imp
	it.Status.SetText(st)
	if it.fixBtn != nil {
		if info != nil && info.Fix != nil && info.Problem != "" {
			it.fixBtn.SetText(info.Fix.Label)
			it.fixBtn.Show()
		} else {
			it.fixBtn.Hide()
		}
	}
}

func (in *installer) rebuild() {
	in.rows.Objects = nil
	for _, it := range in.items {
		item := it
		name := widget.NewLabel(filepath.Base(item.Path))
		name.Truncation = fyne.TextTruncateEllipsis
		rm := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			if in.run {
				return
			}
			for i, x := range in.items {
				if x == item {
					in.items = append(in.items[:i], in.items[i+1:]...)
					break
				}
			}
			in.rebuild()
		})
		rm.Importance = widget.LowImportance
		insp := widget.NewButtonWithIcon("", theme.InfoIcon(), func() { in.showDiag(item) })
		insp.Importance = widget.LowImportance
		item.fixBtn = widget.NewButtonWithIcon("Fix", theme.SettingsIcon(), func() { in.fix(item) })
		item.fixBtn.Importance = widget.WarningImportance
		in.updateStatus(item)
		idBox := container.NewGridWrap(fyne.NewSize(140, 36), item.ID)
		left := container.NewHBox(rm, insp, idBox, item.DVD)
		center := container.NewGridWithColumns(3, item.Title, name, container.NewBorder(nil, nil, nil, item.fixBtn, item.Status))
		rowBG := canvas.NewRectangle(colCardHi)
		rowBG.CornerRadius = 6
		in.rows.Add(container.NewStack(rowBG, container.NewPadded(container.NewBorder(nil, nil, left, nil, center))))
	}
	in.rows.Refresh()
	if len(in.items) == 0 {
		in.count.SetText(emptyQueueText())
	} else {
		in.count.SetText(fmt.Sprintf("%d files queued", len(in.items)))
	}
}

func (in *installer) showDiag(it *queueItem) {
	go func() {
		txt := "Format: " + it.format.Name() + "\n\n"
		switch {
		case it.probeErr != "":
			txt += "Error: " + it.probeErr
		case it.info.Details != "":
			txt += it.info.Details
		}
		if formats.IsNative(it.format) {
			if out, e := in.a.hdl.CDVDInfo(context.Background(), it.Path); out != "" {
				txt += "\nhdl_dump cdvd_info:\n" + strings.TrimSpace(out)
				if e != nil {
					txt += "\n→ " + e.Error()
				}
			}
		}
		fyne.Do(func() {
			g := widget.NewTextGridFromString(txt)
			s := container.NewScroll(g)
			s.SetMinSize(fyne.NewSize(860, 380))
			dialog.ShowCustom("Image diagnostics", "Close", s, in.w)
		})
	}()
}

// fix runs the plug-in's repair and swaps the repaired file into the queue.
func (in *installer) fix(it *queueItem) {
	if in.run || it.info == nil || it.info.Fix == nil {
		return
	}
	fx := it.info.Fix
	dialog.ShowConfirm("Repair image", fx.Confirm, func(ok bool) {
		if !ok {
			return
		}
		go func() {
			in.run = true
			fyne.Do(func() { in.start.Disable(); it.Status.SetText("Repairing…") })
			dst, err := fx.Run(context.Background(), func(f float64) { fyne.Do(func() { in.prog.SetValue(f) }) })
			in.run = false
			if err == nil {
				if f, e := formats.For(dst); e == nil {
					if info, e := f.Probe(context.Background(), dst); e == nil {
						it.format, it.info, it.probeErr = f, info, ""
					}
				}
				it.Path = dst
				in.a.log("Repaired copy written: " + dst)
			} else {
				in.a.log("Repair failed: " + err.Error())
			}
			fyne.Do(func() {
				in.start.Enable()
				in.rebuild()
				if err != nil {
					dialog.ShowError(err, in.w)
				}
			})
		}()
	}, in.w)
}

func (in *installer) install() {
	a := in.a
	if a.cfg.Device == "" {
		fyne.Do(func() { dialog.ShowInformation("Disk", "Select a PS2 disk in the main window first.", in.w) })
		return
	}
	if !a.beginWrite("Installing games…", "Installing games") {
		return
	}
	in.run = true
	fyne.Do(func() { in.start.Disable() })
	okN, errN, skipN := 0, 0, 0
	var installedIDs []string
	onDisk := map[string]bool{}
	for _, g := range a.gamesSnapshot() {
		onDisk[strings.ToUpper(g.Name)] = true
	}
	var installed []tools.Game
	var todo []*queueItem
	for _, it := range in.items {
		if !it.done {
			todo = append(todo, it)
		}
	}
	// fetch compatibility info and covers for every known ID in parallel up front
	var known []string
	fyne.DoAndWait(func() {
		for _, it := range todo {
			if id := strings.TrimSpace(it.ID.Text); id != "" {
				known = append(known, id)
			}
		}
	})
	what := 0
	if a.cfg.CompatEnabled() {
		what |= fetchInfo
	}
	if a.cfg.CoversEnabled() {
		what |= fetchArt
	}
	if a.cfg.OSDEnabled() {
		what |= fetchIcons
	}
	if len(known) > 0 && what != 0 {
		a.setStatus("Fetching game info, pictures and icons…")
		a.prefetch(known, what)
	}
	hdrTmp, _ := os.MkdirTemp("", "ps2hdl-hdr")
	defer os.RemoveAll(hdrTmp)
	for i, it := range todo {
		var title, id string
		var dvd, auto bool
		fyne.DoAndWait(func() { // must block: values are used right below
			title = gamedb.CleanTitle(it.Title.Text)
			id = strings.TrimSpace(it.ID.Text)
			dvd = it.DVD.Checked
			auto = it.titleAuto
		})
		skip := func(log, status string) {
			skipN++
			a.log("Skipped " + filepath.Base(it.Path) + ": " + log)
			if status != "" {
				fyne.Do(func() { it.Status.Importance = widget.DangerImportance; it.Status.SetText("Skipped: " + status) })
			}
		}
		info := it.info
		switch {
		case it.probeErr != "":
			skip(it.probeErr, "")
			continue
		case info.Unsupported != "":
			skip(info.Unsupported, "")
			continue
		case info.Problem != "":
			skip(info.Problem, info.Problem)
			continue
		}
		// the plug-in turns the file into something hdl_dump can read
		env := formats.Env{
			WorkDir: a.workDir(), Startup: id, Log: a.log,
			Progress: func(p float64) { fyne.Do(func() { in.prog.SetValue(p); a.progress.SetValue(p) }) },
			Status: func(s string) {
				fyne.Do(func() { it.Status.Importance = widget.MediumImportance; it.Status.SetText(s) })
				a.setStatus(fmt.Sprintf("%d/%d %s: %s", i+1, len(todo), filepath.Base(it.Path), s))
			},
		}
		prep, err := it.format.Prepare(context.Background(), it.Path, info, env)
		if err == nil && !formats.IsNative(it.format) {
			// only plain 2048-byte sectors are ever written to the PS2 disk
			if verr := disc.Verify2048(prep.Path); verr != nil {
				prep.Close()
				err = fmt.Errorf("prepared data is not a 2048-byte ISO: %w", verr)
			}
		}
		if err != nil {
			errN++
			a.logProblem("ERROR", it.Path, it.format.Name(), "install failed: "+err.Error(), info.Details)
			fyne.Do(func() { it.Status.Importance = widget.DangerImportance; it.Status.SetText("ERROR: " + err.Error()) })
			continue
		}
		if id == "" && prep.Startup != "" {
			id = prep.Startup
			sid := id
			fyne.Do(func() { it.ID.SetText(sid) })
		}
		switch prep.Media {
		case formats.DVD:
			dvd = true
		case formats.CD:
			dvd = false
		}
		if n := disc.NormalizeSerial(id); n != "" {
			id = disc.StartupFromSerial(n)
		}
		// title follows the online DB unless the user typed one
		if t, ok := a.db.Lookup(id); ok && (auto || title == "") {
			if ct := gamedb.CleanTitle(t); ct != title {
				title = ct
				fyne.Do(func() { it.setTitle(ct) })
			}
		}
		if (info.NeedsID || prep.NeedsID) && id == "" {
			prep.Close()
			skip("startup ID required", "enter the startup ID")
			continue
		}
		src := prep.Path
		if title == "" {
			title = id
		}
		if onDisk[strings.ToUpper(title)] {
			skipN++
			prep.Close()
			a.log(fmt.Sprintf("Skipped %s: a game named %q is already on the disk (rename one of them)", filepath.Base(it.Path), title))
			fyne.Do(func() {
				it.Status.Importance = widget.WarningImportance
				it.Status.SetText("Skipped: “" + title + "” is already on the disk — change the title to install it")
			})
			continue
		}
		fyne.Do(func() { it.Status.Importance = widget.MediumImportance; it.Status.SetText("Installing…") })
		a.setStatus(fmt.Sprintf("Installing %d/%d: %s", i+1, len(todo), title))
		a.log(fmt.Sprintf("Installing %q (%s) from %s", title, id, src))
		flags := ""
		if a.cfg.CompatEnabled() && id != "" {
			if gi, _ := a.info.Get(id); gi != nil && gi.ModesMask != 0 {
				flags = gamedb.FlagsString(gi.ModesMask) // HD Loader compat modes set at install time
				a.log(fmt.Sprintf("Compatibility modes %s for %s", flags, title))
			}
		}
		hdr := ""
		if hdrTmp != "" {
			hdr = a.headerDir(hdrTmp, title, id) // HDD-OSD title + 3D icon
		}
		err = a.hdl.InjectWithHeader(context.Background(), a.cfg.Device, title, src, id, flags, a.cfg.DMAMode, dvd, hdr,
			func(line string) {
				if p, ok := tools.ParsePercent(line); ok {
					fyne.Do(func() { in.prog.SetValue(p); a.progress.SetValue(p) })
					return
				}
				a.log(line)
			})
		prep.Close() // free the (possibly multi-GB) temporary image right away
		fyne.Do(func() {
			if err != nil {
				it.Status.Importance = widget.DangerImportance
				it.Status.SetText("ERROR: " + err.Error())
			} else {
				it.Status.Importance = widget.SuccessImportance
				it.Status.SetText("✔ Installed")
				it.done = true
			}
		})
		if err != nil {
			errN++
			a.log("ERROR: " + err.Error())
		} else {
			okN++
			onDisk[strings.ToUpper(title)] = true
			if id != "" {
				installedIDs = append(installedIDs, id)
				installed = append(installed, tools.Game{Startup: id, Name: title, Type: map[bool]string{true: "DVD", false: "CD"}[dvd], Flags: flags})
			}
		}
	}
	coversN := 0
	if len(installed) > 0 && (a.cfg.CompatEnabled() || a.cfg.CoversEnabled()) {
		a.setStatus("Writing OPL game info and pictures…")
		cfgN, covN, err := a.postInstall(installed, a.cfg.CompatEnabled(), a.cfg.CoversEnabled())
		if err != nil {
			a.log("OPL info/covers not written: " + err.Error())
		} else {
			coversN = covN
			_ = cfgN
		}
	}
	in.run = false
	fyne.Do(func() { in.start.Enable(); in.prog.SetValue(1) })
	a.end(fmt.Sprintf("Install complete: %d ok, %d errors, %d skipped", okN, errN, skipN))
	fyne.Do(func() {
		dialog.ShowInformation("Install", fmt.Sprintf("Done.\nInstalled: %d\nErrors: %d\nSkipped: %d\nWith OPL pictures: %d", okN, errN, skipN, coversN), in.w)
	})
	a.refreshGames()
}

// emptyQueueText lists the registered formats.
func emptyQueueText() string {
	var names []string
	for _, f := range formats.All() {
		names = append(names, f.Name())
	}
	return "Queue empty — drop images here or use the buttons. Formats: " + strings.Join(names, ", ")
}

// fileFacts describes a file that could not be read as an image.
func fileFacts(p string) string {
	st, err := os.Stat(p)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("Size: %d bytes (%.1f MB)\nExtension: %s", st.Size(), float64(st.Size())/(1<<20), filepath.Ext(p))
}
