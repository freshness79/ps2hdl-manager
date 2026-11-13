package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Sidebar colours: a shade darker than the rest of the UI.
var (
	colNav      = color.NRGBA{0x0b, 0x0d, 0x10, 0xff}
	colNavHover = color.NRGBA{0x1b, 0x1f, 0x26, 0xff}
	colNavText  = color.NRGBA{0xc9, 0xcd, 0xd5, 0xff}
	colNavIcon  = color.NRGBA{0x7c, 0x83, 0x90, 0xff}
)

const navHeight = 34

// navItem is a sidebar entry with hover highlight, accent bar and pointer cursor.
type navItem struct {
	widget.BaseWidget
	label   string
	icon    fyne.Resource
	accent  color.Color // nil = plain item; otherwise a filled "primary" pill
	onTap   func()
	hovered bool
	pressed bool
}

func newNavItem(label string, icon fyne.Resource, accent color.Color, tap func()) *navItem {
	n := &navItem{label: label, icon: icon, accent: accent, onTap: tap}
	n.ExtendBaseWidget(n)
	return n
}

func (n *navItem) Tapped(*fyne.PointEvent) {
	if n.onTap != nil {
		n.onTap()
	}
}
func (n *navItem) MouseIn(*desktop.MouseEvent)    { n.hovered = true; n.Refresh() }
func (n *navItem) MouseMoved(*desktop.MouseEvent) {}
func (n *navItem) MouseOut()                      { n.hovered, n.pressed = false, false; n.Refresh() }
func (n *navItem) MouseDown(*desktop.MouseEvent)  { n.pressed = true; n.Refresh() }
func (n *navItem) MouseUp(*desktop.MouseEvent)    { n.pressed = false; n.Refresh() }
func (n *navItem) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (n *navItem) CreateRenderer() fyne.WidgetRenderer {
	r := &navRenderer{n: n}
	r.bg = canvas.NewRectangle(color.Transparent)
	r.bg.CornerRadius = 6
	r.bar = canvas.NewRectangle(colAccent)
	r.bar.CornerRadius = 2
	r.icon = canvas.NewImageFromResource(nil)
	r.icon.FillMode = canvas.ImageFillContain
	r.text = canvas.NewText(n.label, colNavText)
	r.text.TextSize = 13
	r.objs = []fyne.CanvasObject{r.bg, r.bar, r.icon, r.text}
	r.Refresh()
	return r
}

type navRenderer struct {
	n    *navItem
	bg   *canvas.Rectangle
	bar  *canvas.Rectangle
	icon *canvas.Image
	text *canvas.Text
	objs []fyne.CanvasObject
}

func (r *navRenderer) Layout(s fyne.Size) {
	r.bg.Resize(s)
	r.bg.Move(fyne.NewPos(0, 0))
	r.bar.Resize(fyne.NewSize(3, 18))
	r.bar.Move(fyne.NewPos(0, (s.Height-18)/2))
	r.icon.Resize(fyne.NewSize(18, 18))
	r.icon.Move(fyne.NewPos(14, (s.Height-18)/2))
	ts := r.text.MinSize()
	r.text.Resize(ts)
	r.text.Move(fyne.NewPos(42, (s.Height-ts.Height)/2))
}

func (r *navRenderer) MinSize() fyne.Size {
	return fyne.NewSize(42+r.text.MinSize().Width+12, navHeight)
}

func (r *navRenderer) Refresh() {
	n := r.n
	textCol := colNavText
	switch {
	case n.accent != nil:
		c := toNRGBA(n.accent)
		if n.hovered {
			c = lighten(c, 0.14)
		}
		if n.pressed {
			c = lighten(c, -0.10)
		}
		r.bg.FillColor = c
		r.bar.Hide()
		textCol = color.NRGBA{0xff, 0xff, 0xff, 0xff}
		r.icon.Resource = theme.NewColoredResource(n.icon, theme.ColorNameForegroundOnPrimary)
	case n.hovered:
		r.bg.FillColor = colNavHover
		if n.pressed {
			r.bg.FillColor = lighten(colNavHover, 0.08)
		}
		r.bar.Show()
		textCol = color.NRGBA{0xff, 0xff, 0xff, 0xff}
		r.icon.Resource = theme.NewColoredResource(n.icon, theme.ColorNamePrimary)
	default:
		r.bg.FillColor = color.Transparent
		r.bar.Hide()
		r.icon.Resource = theme.NewColoredResource(n.icon, theme.ColorNamePlaceHolder)
	}
	r.text.Color = textCol
	r.text.TextStyle = fyne.TextStyle{Bold: n.accent != nil}
	r.bg.Refresh()
	r.bar.Refresh()
	r.icon.Refresh()
	r.text.Refresh()
}

func (r *navRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *navRenderer) Destroy()                     {}

func toNRGBA(c color.Color) color.NRGBA {
	if n, ok := c.(color.NRGBA); ok {
		return n
	}
	r, g, b, a := c.RGBA()
	return color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// lighten mixes c with white (f>0) or black (f<0).
func lighten(c color.NRGBA, f float64) color.NRGBA {
	mix := func(v uint8) uint8 {
		if f >= 0 {
			return uint8(float64(v) + (255-float64(v))*f)
		}
		return uint8(float64(v) * (1 + f))
	}
	return color.NRGBA{mix(c.R), mix(c.G), mix(c.B), c.A}
}

// navSection is a small spaced caption for sidebar groups.
func navSection(s string) fyne.CanvasObject {
	spaced := ""
	for i, r := range s {
		if i > 0 {
			spaced += " "
		}
		spaced += string(r)
	}
	t := canvas.NewText(spaced, colFaint)
	t.TextSize = 10
	t.TextStyle = fyne.TextStyle{Bold: true}
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(1, 4))
	return &fyne.Container{Layout: navSectionLayout{}, Objects: []fyne.CanvasObject{spacer, t}}
}

type navSectionLayout struct{}

func (navSectionLayout) Layout(o []fyne.CanvasObject, s fyne.Size) {
	t := o[1]
	ts := t.MinSize()
	t.Resize(ts)
	t.Move(fyne.NewPos(14, s.Height-ts.Height-2))
}
func (navSectionLayout) MinSize(o []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(10, o[1].MinSize().Height+12)
}

// navPanel is the dark rounded sidebar background.
func navPanel(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colNav)
	bg.CornerRadius = 8
	bg.StrokeColor = colBorder
	bg.StrokeWidth = 1
	return &fyne.Container{Layout: paddedLayout{8}, Objects: []fyne.CanvasObject{bg, content}}
}

// paddedLayout: first object fills, the second is inset by p.
type paddedLayout struct{ p float32 }

func (l paddedLayout) Layout(o []fyne.CanvasObject, s fyne.Size) {
	o[0].Resize(s)
	o[0].Move(fyne.NewPos(0, 0))
	o[1].Resize(fyne.NewSize(s.Width-2*l.p, s.Height-2*l.p))
	o[1].Move(fyne.NewPos(l.p, l.p))
}
func (l paddedLayout) MinSize(o []fyne.CanvasObject) fyne.Size {
	m := o[1].MinSize()
	return fyne.NewSize(m.Width+2*l.p, m.Height+2*l.p)
}
