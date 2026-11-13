package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/freshness79/ps2hdl-manager/internal/tools"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) workDir() string {
	if a.cfg.WorkDir != "" {
		return a.cfg.WorkDir
	}
	return filepath.Join(os.TempDir(), "ps2hdl-work")
}

// needCHD: CHD support is built in; kept for symmetry with the other checks.
func (a *App) needCHD() bool { return true }

// pickCHDs asks for a .chd file or a folder (recursive) and calls fn with the list.
func (a *App) pickCHDs(title string, extra fyne.CanvasObject, fn func([]string)) {
	var d dialog.Dialog
	collect := func(p string) []string {
		var out []string
		st, err := os.Stat(p)
		if err != nil {
			return nil
		}
		if !st.IsDir() {
			return []string{p}
		}
		_ = filepath.WalkDir(p, func(fp string, de fs.DirEntry, err error) error {
			if err == nil && !de.IsDir() && strings.EqualFold(filepath.Ext(fp), ".chd") {
				out = append(out, fp)
			}
			return nil
		})
		return out
	}
	file := widget.NewButton("Choose a CHD file…", func() {
		d.Hide()
		fd := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err == nil && r != nil {
				p := r.URI().Path()
				r.Close()
				fn(collect(p))
			}
		}, a.win)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".chd", ".CHD"}))
		fd.Resize(fyne.NewSize(850, 560))
		fd.Show()
	})
	folder := widget.NewButton("Choose a folder (all CHDs inside)…", func() {
		d.Hide()
		fd := dialog.NewFolderOpen(func(l fyne.ListableURI, err error) {
			if err == nil && l != nil {
				fn(collect(l.Path()))
			}
		}, a.win)
		fd.Resize(fyne.NewSize(850, 560))
		fd.Show()
	})
	content := container.NewVBox(file, folder)
	if extra != nil {
		content.Add(extra)
	}
	d = dialog.NewCustom(title, "Cancel", content, a.win)
	d.Show()
}

// convertCHDDialog: CD-mode CHDs (2352/2448-byte units) → DVD CHDs with 2048-byte sectors.
func (a *App) convertCHDDialog() {
	if !a.needCHD() {
		return
	}
	replace := widget.NewCheck("Replace the original files (otherwise writes <name>.dvd.chd)", nil)
	info := widget.NewLabel("Each CHD is decompressed, converted to 2048-byte sectors\nand recompressed as a DVD CHD v5 (readable by PCSX2 and other CHD readers).\nOld CHD v3/v4 files are converted too; v5 DVD CHDs are skipped.")
	a.pickCHDs("Convert CHD to 2048-byte DVD CHD", container.NewVBox(info, replace), func(files []string) {
		if len(files) == 0 {
			return
		}
		go a.convertCHDs(files, replace.Checked)
	})
}

func (a *App) convertCHDs(files []string, replace bool) {
	if !a.begin("Converting CHD files…") {
		return
	}
	defer a.busy.Store(false)
	ctx := context.Background()
	okN, skipN, errN := 0, 0, 0
	var saved int64
	prog := func(line string) {
		if p, ok := tools.ParsePercent(line); ok {
			a.ui(func() { a.progress.SetValue(p) })
			return
		}
		a.log(line)
	}
	for i, f := range files {
		name := filepath.Base(f)
		kind, err := a.chd.Kind(ctx, f)
		if err != nil {
			a.log("Skipped " + name + ": " + err.Error())
			skipN++
			continue
		}
		if v, _ := a.chd.Version(f); kind == "DVD" && v == 5 {
			a.log("Skipped " + name + ": already a 2048-byte DVD CHD")
			skipN++
			continue
		}
		a.setStatus(fmt.Sprintf("Converting %d/%d: decompressing %s", i+1, len(files), name))
		iso, err := a.chd.ToISO(ctx, f, a.workDir(), prog)
		if err != nil {
			a.log("ERROR " + name + ": " + err.Error())
			errN++
			continue
		}
		base := strings.TrimSuffix(f, filepath.Ext(f))
		out := base + ".dvd.chd"
		a.setStatus(fmt.Sprintf("Converting %d/%d: compressing %s", i+1, len(files), filepath.Base(out)))
		err = a.chd.CreateDVD(ctx, iso, out, prog)
		os.Remove(iso)
		if err == nil {
			if k, e := a.chd.Kind(ctx, out); e != nil || k != "DVD" {
				err = fmt.Errorf("verification of %s failed", filepath.Base(out))
				os.Remove(out)
			}
		}
		if err != nil {
			a.log("ERROR " + name + ": " + err.Error())
			errN++
			continue
		}
		so, _ := os.Stat(f)
		sn, _ := os.Stat(out)
		if so != nil && sn != nil {
			saved += so.Size() - sn.Size()
		}
		if replace {
			if err := os.Rename(out, f); err != nil {
				a.log("Converted, but could not replace " + name + ": " + err.Error())
			} else {
				out = f
			}
		}
		a.log("Converted: " + name + " → " + filepath.Base(out))
		okN++
	}
	msg := fmt.Sprintf("Converted: %d\nSkipped: %d\nErrors: %d\nSpace difference: %+.1f MB", okN, skipN, errN, -float64(saved)/1024/1024)
	a.setStatus(fmt.Sprintf("CHD conversion: %d converted, %d skipped, %d errors", okN, skipN, errN))
	a.ui(func() { dialog.ShowInformation("CHD conversion", msg, a.win) })
}

// extractCHDDialog: CHD → plain 2048-byte ISO next to the source.
func (a *App) extractCHDDialog() {
	if !a.needCHD() {
		return
	}
	a.pickCHDs("Extract CHD to ISO", widget.NewLabel("The ISO (2048-byte sectors) is written next to each CHD."), func(files []string) {
		if len(files) == 0 {
			return
		}
		go func() {
			if !a.begin("Extracting CHD files…") {
				return
			}
			defer a.busy.Store(false)
			okN, errN := 0, 0
			for i, f := range files {
				a.setStatus(fmt.Sprintf("Extracting %d/%d: %s", i+1, len(files), filepath.Base(f)))
				iso, err := a.chd.ToISO(context.Background(), f, filepath.Dir(f), a.hdlLine)
				if err != nil {
					a.log("ERROR " + filepath.Base(f) + ": " + err.Error())
					errN++
					continue
				}
				a.log("Extracted: " + iso)
				okN++
			}
			a.setStatus(fmt.Sprintf("CHD extraction: %d ok, %d errors", okN, errN))
			a.ui(func() {
				dialog.ShowInformation("CHD extraction", fmt.Sprintf("Extracted: %d\nErrors: %d", okN, errN), a.win)
			})
		}()
	})
}
