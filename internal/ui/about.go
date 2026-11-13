package ui

import (
	_ "embed"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	legal "github.com/freshness79/ps2hdl-manager"
	"github.com/freshness79/ps2hdl-manager/internal/buildinfo"
	"github.com/freshness79/ps2hdl-manager/internal/config"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// appTagline is shown under the product name and in About.
const appTagline = "Game library manager for PS2 hard drives · OPL & HD Loader"

const appDescription = "PS2 HDL Manager installs PlayStation 2 games (ISO, CUE/BIN, CHD) on a PS2-formatted " +
	"hard drive and keeps the library tidy: titles synced from an online database, cover art and " +
	"per-game OPL settings, compatibility modes, rename, resync and removal of games. " +
	"hdl_dump, pfsshell and a CHD codec are built in: no external tools are required."

const appCopyright = "Copyright © 2026 " + buildinfo.Author + ".\n\nThis program is free software: you can redistribute it and/or modify it under the terms " +
	"of the GNU General Public License as published by the Free Software Foundation, either version 2 " +
	"of the License, or (at your option) any later version.\n\n" +
	"This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; " +
	"without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. " +
	"Writing to disks can destroy data: keep backups."

const appDisclaimer = "This software is provided \"AS IS\", without warranty of any kind. To the maximum extent " +
	"permitted by law, the author is not liable for any damage or loss (including loss of data or damage to " +
	"hardware) arising from its use or misuse. You alone are responsible for how you use it and for complying " +
	"with the laws that apply to you, including copyright law: it is intended for managing legally obtained " +
	"games, such as backups of discs you own. It does not circumvent copy protection and does not provide any " +
	"game, BIOS or firmware. Online content (titles, artwork, icons) comes from third parties and belongs to its owners."

const appTrademarks = "\"PlayStation\" and \"PS2\" are registered trademarks or trademarks of Sony Interactive " +
	"Entertainment Inc. This program is not affiliated with, endorsed or sponsored by Sony. " +
	"Game titles and cover art belong to their respective owners."

type credit struct{ name, role, license, link string }

var builtIn = []credit{
	{"hdl-dump", "HDL game installation and APA partition handling (built in)", "GPL-2.0-or-later", "https://github.com/ps2homebrew/hdl-dump"},
	{"pfsshell / ps2sdk", "PFS file system access: OPL partition, covers, CFG files (built in)", "GPL-2.0 · AFL-2.0", "https://github.com/ps2homebrew/pfsshell"},
	{"MAME CHD format", "Algorithms of the CHD reader/writer and CD ECC tables", "BSD-3-Clause", "https://github.com/mamedev/mame"},
	{"Fyne", "User interface toolkit (incl. Noto Sans fonts)", "BSD-3-Clause · OFL-1.1", "https://fyne.io"},
	{"klauspost/compress, ulikunitz/xz", "zstd and LZMA codecs", "BSD-3-Clause · BSD-2-Clause", "https://github.com/klauspost/compress"},
	{"Go", "Language runtime and standard library", "BSD-3-Clause", "https://go.dev"},
}

var dataSources = []credit{
	{"GameDB-PS2", "Game titles, genres, developers, publishers, release dates (niemasd)", "", "https://github.com/niemasd/GameDB-PS2"},
	{"PS2-OPL-CFG-Database", "Game information: developer, players, description (Tom-Bruise)", "", "https://github.com/Tom-Bruise/PS2-OPL-CFG-Database"},
	{"PS2-OPL-CFG-Compatibility-Database", "Recommended compatibility modes (GDX-X)", "", "https://github.com/GDX-X/PS2-OPL-CFG-Compatibility-Database"},
	{"ps2-covers", "Cover art (xlenore)", "", "https://github.com/xlenore/ps2-covers"},
	{"OPL Manager art backup", "OPL art: icons, backgrounds, screenshots, back covers, spines, logos (OPL Manager community, archive.org)", "", "https://archive.org/details/OPLM_ART_2024_09"},
	{"Icon pack for HDL Batch installer", "HDD-OSD 3D icons (luissilva1044894)", "MIT", "https://github.com/luissilva1044894/Icon-pack-for-HDL-Batch-installer"},
}

func (a *App) about() {
	logo := appLogo(56)
	name := canvas.NewText(appTitle, colText)
	name.TextSize = 20
	name.TextStyle = fyne.TextStyle{Bold: true}
	ver := canvas.NewText(fmt.Sprintf("Version %s  ·  %s/%s", buildinfo.Version, runtime.GOOS, runtime.GOARCH), colMuted)
	ver.TextSize = 12
	tag := canvas.NewText(appTagline, colAccent)
	tag.TextSize = 12
	head := container.NewHBox(
		logo,
		container.NewVBox(name, tag, ver))

	para := func(s string) *widget.Label {
		l := widget.NewLabel(s)
		l.Wrapping = fyne.TextWrapWord
		return l
	}
	small := func(s string) *widget.Label {
		l := para(s)
		l.SizeName = theme.SizeNameCaptionText
		l.Importance = widget.LowImportance
		return l
	}
	creditRows := func(list []credit) fyne.CanvasObject {
		box := container.NewVBox()
		for _, c := range list {
			title := c.name
			if c.license != "" {
				title += "  —  " + c.license
			}
			var link fyne.CanvasObject = widget.NewLabel(c.link)
			if u, err := url.Parse(c.link); err == nil {
				link = widget.NewHyperlink(c.link, u)
			}
			box.Add(container.NewVBox(
				widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				small(c.role),
				link,
			))
		}
		return box
	}

	source := "The complete corresponding source code is distributed together with this program."
	var sourceLink fyne.CanvasObject = layout.NewSpacer()
	if buildinfo.SourceURL != "" {
		source = "The complete corresponding source code is available at:"
		if u, err := url.Parse(buildinfo.SourceURL); err == nil {
			sourceLink = widget.NewHyperlink(buildinfo.SourceURL, u)
		}
	}

	body := container.NewVBox(
		para(appDescription),
		hairline(),
		sectionLabel("LICENSE"),
		para(appCopyright),
		para(source), sourceLink,
		donateRow(),
		hairline(),
		sectionLabel("BUILT WITH"),
		creditRows(builtIn),
		small("Local fixes to the bundled hdl-dump sources are listed in third_party/PATCHES.md."),
		hairline(),
		sectionLabel("ONLINE DATA (downloaded at run time, subject to each project's terms)"),
		creditRows(dataSources),
		hairline(),
		sectionLabel("DISCLAIMER"),
		small(appDisclaimer),
		hairline(),
		sectionLabel("TRADEMARKS"),
		small(appTrademarks),
		hairline(),
		sectionLabel("FILES"),
		small("Settings: "+config.Path()+"\nActivity log: "+filepath.Join(config.Dir(), "ps2hdl.log")),
	)
	scroll := container.NewVScroll(container.NewPadded(body))
	scroll.SetMinSize(fyne.NewSize(640, 520))

	var d dialog.Dialog
	gpl := widget.NewButtonWithIcon("GNU GPL text", theme.DocumentIcon(), func() {
		a.licenseWindow("GNU General Public License", legal.License)
	})
	tp := widget.NewButtonWithIcon("Third-party licenses", theme.ListIcon(), func() {
		a.licenseWindow("Third-party software notices", legal.ThirdPartyNotices)
	})
	closeBtn := widget.NewButton("Close", func() { d.Hide() })
	closeBtn.Importance = widget.HighImportance
	buttons := container.NewHBox(gpl, tp, layout.NewSpacer(), closeBtn)

	content := container.NewBorder(container.NewVBox(head, hairline()), buttons, nil, nil, scroll)
	d = dialog.NewCustomWithoutButtons("About "+appTitle, content, a.win)
	d.Resize(fyne.NewSize(720, 680))
	d.Show()
}

// licenseWindow shows a long license text in its own window, with a filter and
// a button to save a copy.
func (a *App) licenseWindow(title, text string) {
	w := a.fa.NewWindow(title)
	lines := strings.Split(strings.ReplaceAll(text, "\t", "    "), "\n")
	list := widget.NewList(
		func() int { return len(lines) },
		func() fyne.CanvasObject {
			t := canvas.NewText("", colText)
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.TextSize = 12
			return container.New(layout.NewCustomPaddedLayout(1, 1, 10, 10), t)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			t := o.(*fyne.Container).Objects[0].(*canvas.Text)
			t.Text = lines[i]
			t.Refresh()
		})
	list.HideSeparators = true
	search := widget.NewEntry()
	search.SetPlaceHolder("Find component or text (Enter = next match)…")
	last := -1
	search.OnSubmitted = func(q string) {
		q = strings.ToLower(strings.TrimSpace(q))
		if q == "" {
			return
		}
		for k := 1; k <= len(lines); k++ {
			i := (last + k) % len(lines)
			if strings.Contains(strings.ToLower(lines[i]), q) {
				last = i
				list.Select(i)
				list.ScrollTo(i)
				return
			}
		}
	}
	save := widget.NewButtonWithIcon("Save a copy…", theme.DocumentSaveIcon(), func() {
		fd := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
			if err != nil || wc == nil {
				return
			}
			defer wc.Close()
			if _, err := wc.Write([]byte(text)); err != nil {
				dialog.ShowError(err, w)
			}
		}, w)
		fd.SetFileName(strings.ReplaceAll(title, " ", "_") + ".txt")
		fd.Show()
	})
	closeBtn := widget.NewButton("Close", w.Close)
	top := container.NewBorder(nil, nil, nil, container.NewHBox(save, closeBtn), search)
	w.SetContent(container.NewBorder(container.NewPadded(top), nil, nil, nil, list))
	w.Resize(fyne.NewSize(900, 700))
	w.Show()
}

// appIcon is the application icon (assets/icon).
var appIcon = fyne.NewStaticResource("ps2hdl-manager.png", legal.AppIcon)

// appLogo shows the application icon at size×size.
func appLogo(size float32) *canvas.Image {
	img := canvas.NewImageFromResource(appIcon)
	img.FillMode = canvas.ImageFillContain
	img.ScaleMode = canvas.ImageScaleSmooth
	img.SetMinSize(fyne.NewSize(size, size))
	return img
}

// donateRow is the optional "buy me a coffee" link (buildinfo.DonateURL).
func donateRow() fyne.CanvasObject {
	u, err := url.Parse(buildinfo.DonateURL)
	if buildinfo.DonateURL == "" || err != nil {
		return layout.NewSpacer()
	}
	btn := widget.NewButtonWithIcon("Buy me a coffee", theme.ContentAddIcon(), func() { _ = fyne.CurrentApp().OpenURL(u) })
	btn.Importance = widget.HighImportance
	return container.NewVBox(
		hairline(),
		sectionLabel("SUPPORT"),
		widget.NewLabel("This program is free and will stay free. If it saves you time, you can buy the author a coffee: entirely optional."),
		container.NewHBox(btn),
	)
}
