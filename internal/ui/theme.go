package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

// Palette: neutral graphite surfaces, one blue accent, semantic status colours.
var (
	colBG     = color.NRGBA{0x0f, 0x11, 0x15, 0xff}
	colCard   = color.NRGBA{0x16, 0x19, 0x1f, 0xff}
	colCardHi = color.NRGBA{0x1e, 0x22, 0x2a, 0xff}
	colBorder = color.NRGBA{0x29, 0x2e, 0x38, 0xff}
	colInput  = color.NRGBA{0x1b, 0x1f, 0x26, 0xff}
	colText   = color.NRGBA{0xe6, 0xe8, 0xec, 0xff}
	colMuted  = color.NRGBA{0x8b, 0x92, 0x9e, 0xff}
	colFaint  = color.NRGBA{0x5c, 0x63, 0x70, 0xff}

	colAccent   = color.NRGBA{0x4a, 0x8c, 0xff, 0xff}
	colAccentLo = color.NRGBA{0x4a, 0x8c, 0xff, 0x2e} // tinted backgrounds, selection
	colGreen    = color.NRGBA{0x3f, 0xb2, 0x7f, 0xff}
	colGreenLo  = color.NRGBA{0x3f, 0xb2, 0x7f, 0x2e}
	colAmber    = color.NRGBA{0xe0, 0xa6, 0x3a, 0xff}
	colRed      = color.NRGBA{0xe5, 0x53, 0x4b, 0xff}
	colNeutral  = color.NRGBA{0x2a, 0x2f, 0x3a, 0xff} // neutral pills
	colRowSel   = color.NRGBA{0x4a, 0x8c, 0xff, 0x24}
)

type slickTheme struct{}

var _ fyne.Theme = slickTheme{}

func (slickTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBG
	case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground, theme.ColorNameHeaderBackground:
		return colCardHi
	case theme.ColorNameInputBackground:
		return colInput
	case theme.ColorNameInputBorder:
		return colBorder
	case theme.ColorNameButton:
		return colCardHi
	case theme.ColorNameDisabledButton:
		return colCard
	case theme.ColorNamePrimary:
		return colAccent
	case theme.ColorNameHyperlink:
		return colAccent
	case theme.ColorNameFocus:
		return color.NRGBA{0x4a, 0x8c, 0xff, 0x99}
	case theme.ColorNameSelection:
		return colAccentLo
	case theme.ColorNameHover:
		return color.NRGBA{0xff, 0xff, 0xff, 0x0f}
	case theme.ColorNamePressed:
		return color.NRGBA{0xff, 0xff, 0xff, 0x1c}
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnSuccess, theme.ColorNameForegroundOnError, theme.ColorNameForegroundOnWarning:
		return color.White
	case theme.ColorNamePlaceHolder:
		return colMuted
	case theme.ColorNameDisabled:
		return colFaint
	case theme.ColorNameSeparator:
		return color.NRGBA{0x1f, 0x23, 0x2b, 0xff}
	case theme.ColorNameScrollBar:
		return color.NRGBA{0xff, 0xff, 0xff, 0x30}
	case theme.ColorNameScrollBarBackground:
		return color.NRGBA{0, 0, 0, 0}
	case theme.ColorNameShadow:
		return color.NRGBA{0, 0, 0, 0x80}
	case theme.ColorNameSuccess:
		return colGreen
	case theme.ColorNameError:
		return colRed
	case theme.ColorNameWarning:
		return colAmber
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (slickTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameSubHeadingText:
		return 15
	case theme.SizeNameInputRadius:
		return 6
	case theme.SizeNameSelectionRadius:
		return 4
	case theme.SizeNameScrollBarRadius:
		return 3
	case theme.SizeNameScrollBar:
		return 6
	case theme.SizeNameScrollBarSmall:
		return 3
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameSeparatorThickness:
		return 1
	}
	return theme.DefaultTheme().Size(n)
}

func (slickTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (slickTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

// ---------------------------------------------------------------- building blocks

// card wraps content in a rounded panel with a thin border.
func card(obj fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colCard)
	bg.CornerRadius = 8
	bg.StrokeColor = colBorder
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.NewPadded(obj))
}

// hairline is a 1px horizontal rule.
func hairline() fyne.CanvasObject {
	r := canvas.NewRectangle(colBorder)
	r.SetMinSize(fyne.NewSize(10, 1))
	return r
}

// sectionLabel is a small, spaced, muted caption.
func sectionLabel(s string) fyne.CanvasObject {
	t := canvas.NewText(s, colMuted)
	t.TextSize = 10
	t.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewPadded(t)
}

// statTile is a dashboard tile: caption, big value, accent bar.
type statTile struct {
	value *canvas.Text
	sub   *canvas.Text
	obj   fyne.CanvasObject
}

func newStatTile(caption string, icon fyne.Resource) *statTile {
	cap := canvas.NewText(caption, colMuted)
	cap.TextSize = 10
	cap.TextStyle = fyne.TextStyle{Bold: true}
	val := canvas.NewText("—", colText)
	val.TextSize = 22
	val.TextStyle = fyne.TextStyle{Bold: true}
	sub := canvas.NewText("", colMuted)
	sub.TextSize = 11
	ic := canvas.NewImageFromResource(theme.NewColoredResource(icon, theme.ColorNamePlaceHolder))
	ic.FillMode = canvas.ImageFillContain
	ic.SetMinSize(fyne.NewSize(16, 16))
	head := container.NewBorder(nil, nil, nil, ic, cap)
	content := container.NewVBox(head, val, sub)
	return &statTile{value: val, sub: sub, obj: card(content)}
}

func (s *statTile) Set(v, sub string) {
	s.value.Text = v
	s.sub.Text = sub
	s.value.Refresh()
	s.sub.Refresh()
}

// usageBar is a rounded two-tone capacity bar (teal → violet fill).
type usageBar struct {
	track *canvas.Rectangle
	fill  *canvas.Rectangle
	frac  float64
	warn  bool // capacity bar: amber/red when nearly full
	obj   *fyne.Container
}

func newUsageBar() *usageBar {
	u := &usageBar{}
	u.track = canvas.NewRectangle(colInput)
	u.track.CornerRadius = 3
	u.fill = canvas.NewRectangle(colAccent)
	u.fill.CornerRadius = 3
	u.obj = container.New(&usageLayout{u: u}, u.track, u.fill)
	return u
}

func (u *usageBar) Set(f float64) {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	u.frac = f
	u.fill.FillColor = colAccent
	if u.warn && f >= 0.95 {
		u.fill.FillColor = colRed
	} else if u.warn && f >= 0.85 {
		u.fill.FillColor = colAmber
	}
	u.obj.Refresh()
}

type usageLayout struct{ u *usageBar }

func (l *usageLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	h := float32(6)
	y := (size.Height - h) / 2
	l.u.track.Move(fyne.NewPos(0, y))
	l.u.track.Resize(fyne.NewSize(size.Width, h))
	l.u.fill.Move(fyne.NewPos(0, y))
	l.u.fill.Resize(fyne.NewSize(size.Width*float32(l.u.frac), h))
}

func (l *usageLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(60, 12) }

// SetValue lets usageBar be used as a slim progress bar.
func (u *usageBar) SetValue(f float64) { u.Set(f) }

// fixedWidth gives its single child a fixed width and full height.
type fixedWidth struct{ w float32 }

func (f fixedWidth) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(f.w, size.Height))
	}
}

func (f fixedWidth) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		if m := o.MinSize(); m.Height > h {
			h = m.Height
		}
	}
	return fyne.NewSize(f.w, h)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
