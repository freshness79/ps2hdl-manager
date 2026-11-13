package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/freshness79/ps2hdl-manager/internal/disc"
	"github.com/freshness79/ps2hdl-manager/internal/gamedb"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------- details panel

type detailsPanel struct {
	img     *canvas.Image
	empty   *canvas.Text
	name    *widget.Label
	id      *canvas.Text
	info    *canvas.Text
	meta    *widget.Label
	box     *fyne.Container // panel content: re-laid out when texts change height
	compat  *widget.Label
	game    tools.Game
	obj     fyne.CanvasObject
	current string // startup ID shown
}

func (a *App) buildDetails() fyne.CanvasObject {
	d := &detailsPanel{}
	frame := canvas.NewRectangle(colInput)
	frame.CornerRadius = 6
	frame.SetMinSize(fyne.NewSize(gamedb.CoverW+20, gamedb.CoverH+20))
	d.img = canvas.NewImageFromResource(nil)
	d.img.FillMode = canvas.ImageFillContain
	d.img.SetMinSize(fyne.NewSize(gamedb.CoverW, gamedb.CoverH))
	d.empty = canvas.NewText("no cover", colMuted)
	d.empty.TextSize = 12
	d.empty.Alignment = fyne.TextAlignCenter
	d.name = widget.NewLabelWithStyle("Select a game", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	d.name.Wrapping = fyne.TextWrapWord
	d.id = canvas.NewText("", colAccent)
	d.id.TextSize = 13
	d.id.TextStyle = fyne.TextStyle{Bold: true}
	d.id.Alignment = fyne.TextAlignCenter
	d.info = canvas.NewText("", colMuted)
	d.info.TextSize = 11
	d.info.Alignment = fyne.TextAlignCenter

	d.meta = widget.NewLabel("")
	d.meta.Alignment = fyne.TextAlignCenter
	d.meta.Wrapping = fyne.TextWrapWord
	d.meta.SizeName = theme.SizeNameCaptionText
	d.compat = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	d.compat.Wrapping = fyne.TextWrapWord
	infoBtn := widget.NewButtonWithIcon("Info & compatibility", theme.InfoIcon(), a.gameInfoDialog)

	dl := widget.NewButtonWithIcon("Download art", theme.DownloadIcon(), a.downloadSelectedArt)
	pick := widget.NewButtonWithIcon("Choose cover…", theme.FileImageIcon(), a.pickSelectedCover)
	rm := widget.NewButtonWithIcon("Remove art", theme.DeleteIcon(), a.removeSelectedArt)
	rm.Importance = widget.LowImportance

	cover := container.NewCenter(container.NewStack(frame, container.NewCenter(d.empty), container.NewPadded(d.img)))
	d.box = container.NewVBox(
		sectionLabel("COVER ART"),
		cover,
		d.name, d.id, d.info,
		sectionLabel("DETAILS"), d.meta, d.compat, infoBtn,
		layout.NewSpacer(),
		sectionLabel("OPL ART"),
		dl, pick, rm,
	)
	d.obj = container.NewVScroll(card(d.box))
	a.details = d
	return d.obj
}

// showDetails updates the side panel for the selected game, downloading a preview if needed.
func (a *App) showDetails(g *tools.Game) {
	d := a.details
	if d == nil {
		return
	}
	if g == nil {
		d.current = ""
		d.meta.SetText("")
		d.compat.SetText("")
		d.name.SetText("Select a game")
		d.id.Text, d.info.Text = "", ""
		d.img.Resource, d.img.File = nil, ""
		d.empty.Show()
		d.id.Refresh()
		d.info.Refresh()
		d.relayout()
		d.img.Refresh()
		return
	}
	d.current = g.Startup
	d.game = *g
	d.name.SetText(g.Name)
	a.updateDetailsInfo(g.Startup)
	if a.info.Cached(g.Startup) == nil {
		sid := g.Startup
		go func() {
			_, _ = a.info.Get(sid)
			fyne.Do(func() { a.updateDetailsInfo(sid) })
		}()
	}
	d.id.Text = g.Startup
	d.info.Text = fmt.Sprintf("%s · %.2f GB", g.Type, float64(g.SizeKB)/1024/1024)
	d.id.Refresh()
	d.info.Refresh()
	d.relayout()
	if p := gamedb.CachedCover(g.Startup); p != "" {
		a.setCoverImage(p)
		return
	}
	d.img.File = ""
	d.img.Refresh()
	d.empty.Text = "loading…"
	d.empty.Show()
	d.empty.Refresh()
	id := g.Startup
	go func() {
		p, err := gamedb.DownloadCover(a.cfg.CoverURL, id)
		fyne.Do(func() {
			if d.current != id {
				return
			}
			if err != nil {
				d.empty.Text = "no cover online"
				d.empty.Refresh()
				return
			}
			a.setCoverImage(p)
		})
	}()
}

func (a *App) setCoverImage(p string) {
	d := a.details
	d.empty.Hide()
	d.img.File = p
	d.img.Resource = nil
	d.img.Refresh()
}

// uploadArt makes sure the OPL partition exists and copies the given cached
// pictures into ART, deleting the rm names (legacy JPGs) in the same session.
func (a *App) uploadArt(files, rm []string) error {
	if len(files) == 0 && len(rm) == 0 {
		return nil
	}
	_, err := a.pfs.OPLSession(context.Background(), a.cfg.Device, a.cfg.OPLPart, gamedb.ArtCacheDir(),
		artCmds(a.cfg.OPLPart, files, rm), true, a.log)
	return err
}

// cachedArtOf lists the cached pictures of a game (configured types) and the
// legacy names they replace.
func (a *App) cachedArtOf(startup string, keys []string) (files, rm []string) {
	for _, k := range keys {
		if p := gamedb.CachedArt(startup, k); p != "" {
			files = append(files, filepath.Base(p))
			rm = append(rm, gamedb.LegacyArtNames(startup, k)...)
		}
	}
	return files, rm
}

// ---------------------------------------------------------------- actions

// downloadSelectedArt fetches and installs every configured picture type of the selected game.
func (a *App) downloadSelectedArt() {
	if !a.needReady() {
		return
	}
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	go func() {
		if !a.beginWrite("Installing pictures for "+g.Name+"…", "Installing OPL art") {
			return
		}
		keys := a.artKeys()
		have, problems := gamedb.FetchArt(a.artArchive(), a.cfg.CoverURL, g.Startup, keys)
		for _, p := range problems {
			a.log(g.Startup + ": " + p)
		}
		files, rm := a.cachedArtOf(g.Startup, keys)
		var err error
		if len(files) == 0 {
			err = fmt.Errorf("no pictures found for %s", g.Startup)
		} else {
			err = a.uploadArt(files, rm)
		}
		a.end("Ready")
		if err != nil {
			a.showErr(err)
			return
		}
		a.log(fmt.Sprintf("OPL art installed for %s: %s → %s/%s", g.Startup, strings.Join(have, ", "), a.cfg.OPLPart, tools.ArtDir(a.cfg.OPLPart)))
		a.ui(func() { a.showDetails(&g) })
	}()
}

func (a *App) pickSelectedCover() {
	if !a.needReady() {
		return
	}
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		src := r.URI().Path()
		r.Close()
		go func() {
			if !a.beginWrite("Installing cover…", "Installing a cover") {
				return
			}
			p, err := gamedb.ImportCover(src, g.Startup)
			if err == nil {
				err = a.uploadArt([]string{filepath.Base(p)}, gamedb.LegacyArtNames(g.Startup, "COV"))
			}
			a.end("Ready")
			if err != nil {
				a.showErr(err)
				return
			}
			a.log("Cover installed from " + src)
			a.ui(func() { a.setCoverImage(p) })
		}()
	}, a.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".jpg", ".jpeg", ".png", ".gif", ".JPG", ".JPEG", ".PNG"}))
	d.Resize(fyne.NewSize(800, 550))
	d.Show()
}

// removeSelectedArt deletes every picture of the selected game (all types).
func (a *App) removeSelectedArt() {
	if !a.needReady() {
		return
	}
	g, ok := a.selectedGame()
	if !ok {
		return
	}
	go func() {
		if !a.beginWrite("Removing pictures…", "Removing OPL art") {
			return
		}
		err := a.pfs.RmArt(context.Background(), a.cfg.Device, a.cfg.OPLPart, gamedb.AllArtNames(g.Startup), a.log)
		a.end("Ready")
		if err != nil {
			a.showErr(err)
			return
		}
		for _, t := range gamedb.ArtTypes {
			if p := gamedb.CachedArt(g.Startup, t.Key); p != "" {
				_ = os.Remove(p)
			}
		}
		a.log("OPL art removed for " + g.Startup)
		a.ui(func() { a.showDetails(&g) })
	}()
}

// downloadAllArt fetches the configured pictures for every installed game (in
// parallel) and copies them to the disk: one pfsshell session to list ART, one
// to write. Without overwrite only pictures missing on the disk are added.
func (a *App) downloadAllArt(overwrite bool) {
	if a.cfg.Device == "" {
		a.ui(func() { a.needReady() })
		return
	}
	if !a.beginWrite("Installing OPL art…", "Installing OPL art") {
		return
	}
	defer func() { a.busy.Store(false) }()
	ctx := context.Background()
	present := map[string]bool{}
	files, err := a.pfs.ListArt(ctx, a.cfg.Device, a.cfg.OPLPart)
	if err != nil {
		a.setStatus("Error")
		a.showErr(err)
		return
	}
	for _, f := range files {
		present[strings.ToUpper(f)] = true
	}
	keys := a.artKeys()
	if len(keys) == 0 {
		a.setStatus("Ready")
		a.ui(func() { dialog.ShowInformation("OPL art", "No picture types are selected in Settings.", a.win) })
		return
	}
	need := map[string][]string{} // startup → missing keys
	var ids []string
	complete := 0
	for _, g := range a.gamesSnapshot() {
		n := disc.NormalizeSerial(g.Startup)
		if n == "" || need[n] != nil {
			continue
		}
		var miss []string
		for _, k := range keys {
			if overwrite || !present[strings.ToUpper(gamedb.ArtFileName(g.Startup, k))] {
				miss = append(miss, k)
			}
		}
		if len(miss) == 0 {
			complete++
			continue
		}
		need[n] = miss
		ids = append(ids, g.Startup)
	}
	a.setStatus(fmt.Sprintf("Downloading pictures for %d games…", len(ids)))
	a.prefetch(ids, fetchArt)
	var put, rm, missing []string
	gotGames := 0
	for _, id := range ids {
		f, r := a.cachedArtOf(id, need[disc.NormalizeSerial(id)])
		if len(f) == 0 {
			missing = append(missing, id)
			continue
		}
		gotGames++
		put = append(put, f...)
		for _, x := range r {
			if present[strings.ToUpper(x)] {
				rm = append(rm, x) // only legacy files that are really there
			}
		}
	}
	a.setStatus(fmt.Sprintf("Copying %d pictures to %s…", len(put), a.cfg.OPLPart))
	if err := a.uploadArt(put, rm); err != nil {
		a.setStatus("Error")
		a.showErr(err)
		return
	}
	msg := fmt.Sprintf("Pictures installed: %d (for %d games)\nGames already complete: %d\nGames with nothing found: %d\nOld JPG covers replaced: %d",
		len(put), gotGames, complete, len(missing), len(rm))
	a.setStatus(fmt.Sprintf("OPL art: %d pictures installed", len(put)))
	a.log(strings.ReplaceAll(msg, "\n", " · "))
	a.ui(func() { dialog.ShowInformation("OPL art", msg, a.win) })
}

func (a *App) listArt() {
	if !a.needReady() {
		return
	}
	go func() {
		if !a.begin("Reading ART folder…") {
			return
		}
		files, err := a.pfs.ListArt(context.Background(), a.cfg.Device, a.cfg.OPLPart)
		a.end("Ready")
		if err != nil {
			a.showErr(err)
			return
		}
		txt := fmt.Sprintf("%s/%s — %d files\n\n%s", a.cfg.OPLPart, tools.ArtDir(a.cfg.OPLPart), len(files), strings.Join(files, "\n"))
		if legacy := countLegacy(files); legacy > 0 {
			txt = fmt.Sprintf("%d .jpg files are ignored by OPL 1.2 and later: use Artwork → Install OPL art (replace) to convert them.\n\n", legacy) + txt
		}
		a.ui(func() { a.textDialog("OPL art on disk", txt) })
	}()
}

func countLegacy(files []string) int {
	n := 0
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f), ".jpg") {
			n++
		}
	}
	return n
}

func (a *App) createOPLPartition() {
	if !a.needReady() {
		return
	}
	go func() {
		if !a.beginWrite("Creating "+a.cfg.OPLPart+"…", "Creating the OPL partition") {
			return
		}
		err := a.pfs.EnsureOPL(context.Background(), a.cfg.Device, a.cfg.OPLPart, a.log)
		a.end("Ready")
		if err != nil {
			a.showErr(err)
			return
		}
		a.log(a.cfg.OPLPart + " partition and " + tools.ArtDir(a.cfg.OPLPart) + " folder ready.")
		a.ui(func() {
			dialog.ShowInformation("OPL partition", a.cfg.OPLPart+" is ready, with the "+tools.ArtDir(a.cfg.OPLPart)+" folder for cover art.", a.win)
		})
	}()
}

// updateDetailsInfo fills category / players / compatibility in the side panel.
func (a *App) updateDetailsInfo(id string) {
	d := a.details
	if d == nil || d.current != id {
		return
	}
	gi := a.info.Cached(id)
	if gi == nil {
		d.meta.SetText("Loading info…")
		d.compat.SetText("")
		return
	}
	meta := dash(gi.Genre) + "\nMultiplayer: " + gi.MultiplayerText()
	if gi.Developer != "" {
		meta += "\n" + gi.Developer
		if gi.Release != "" {
			meta += " · " + gi.Release
		}
	}
	d.meta.SetText(meta)
	txt, imp := compatSummary(d.game, gi)
	d.compat.Importance = imp
	d.compat.SetText(txt)
	d.relayout()
}

// relayout recomputes the panel layout after a text changed its number of lines
// (otherwise a longer text overlaps the next one).
func (d *detailsPanel) relayout() {
	if d.box != nil {
		d.box.Refresh()
	}
}
