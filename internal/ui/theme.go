// Package ui draws flit's surfaces: the main window, the notch, the tray
// icon. Drawing logic is portable and tested; the Windows parts live in
// _windows.go files.
package ui

import "image/color"

// Palette holds the semantic colour tokens for one theme (dark or light).
// Every surface draws with the active package-level Col* tokens, never with
// literal colours.
type Palette struct {
	Base         color.RGBA // window background, notch
	Sidebar      color.RGBA // navigation column, title bar, tray panel
	Card         color.RGBA // grouped content, floating notes panel
	CardHover    color.RGBA
	Control      color.RGBA // buttons, tracks, unselected controls
	ControlHi    color.RGBA // hovered controls
	Divider      color.RGBA
	Edge         color.RGBA // outlines of floating surfaces
	Text         color.RGBA // primary text, the "on" state of controls
	Muted        color.RGBA // secondary text
	Faint        color.RGBA // captions, disabled
	Ink          color.RGBA // text on primary buttons (ColText fill)
	PrimaryHover color.RGBA // hovered primary button fill
	Live         color.RGBA // the only accent: recording, live
}

// DarkPalette is flit's neutral dark theme (default).
var DarkPalette = Palette{
	Base:         rgb(0x14, 0x14, 0x14),
	Sidebar:      rgb(0x19, 0x19, 0x19),
	Card:         rgb(0x1E, 0x1E, 0x1E),
	CardHover:    rgb(0x24, 0x24, 0x24),
	Control:      rgb(0x2C, 0x2C, 0x2C),
	ControlHi:    rgb(0x36, 0x36, 0x36),
	Divider:      rgb(0x27, 0x27, 0x27),
	Edge:         rgb(0x2D, 0x2D, 0x2D),
	Text:         rgb(0xF4, 0xF4, 0xF4),
	Muted:        rgb(0xA3, 0xA3, 0xA3),
	Faint:        rgb(0x70, 0x70, 0x70),
	Ink:          rgb(0x14, 0x14, 0x14),
	PrimaryHover: rgb(0xDC, 0xDC, 0xDC),
	Live:         rgb(0xF0, 0x3C, 0x52),
}

// LightPalette is flit's crisp neutral light theme.
var LightPalette = Palette{
	Base:         rgb(0xF5, 0xF5, 0xF7),
	Sidebar:      rgb(0xEC, 0xEC, 0xF0),
	Card:         rgb(0xFF, 0xFF, 0xFF),
	CardHover:    rgb(0xF0, 0xF0, 0xF3),
	Control:      rgb(0xE4, 0xE4, 0xE9),
	ControlHi:    rgb(0xD8, 0xD8, 0xDF),
	Divider:      rgb(0xDF, 0xDF, 0xE5),
	Edge:         rgb(0xD1, 0xD1, 0xD8),
	Text:         rgb(0x17, 0x17, 0x1A),
	Muted:        rgb(0x5C, 0x5C, 0x64),
	Faint:        rgb(0x8A, 0x8A, 0x94),
	Ink:          rgb(0xFF, 0xFF, 0xFF),
	PrimaryHover: rgb(0x2D, 0x2D, 0x33),
	Live:         rgb(0xE5, 0x33, 0x49),
}

// Active theme tokens. Swapped atomically on the UI thread by SetTheme.
var (
	ColBase         = DarkPalette.Base
	ColSidebar      = DarkPalette.Sidebar
	ColCard         = DarkPalette.Card
	ColCardHover    = DarkPalette.CardHover
	ColControl      = DarkPalette.Control
	ColControlHi    = DarkPalette.ControlHi
	ColDivider      = DarkPalette.Divider
	ColEdge         = DarkPalette.Edge
	ColText         = DarkPalette.Text
	ColMuted        = DarkPalette.Muted
	ColFaint        = DarkPalette.Faint
	ColInk          = DarkPalette.Ink
	ColPrimaryHover = DarkPalette.PrimaryHover
	ColLive         = DarkPalette.Live

	// Taskbar-independent inks (for the tray icon and white-on-red glyphs).
	ColInkOnDark  = rgb(0xF4, 0xF4, 0xF4)
	ColInkOnLight = rgb(0x1C, 0x1C, 0x1C)

	lightTheme bool
)

// SetTheme activates light or dark mode across all UI colour tokens.
func SetTheme(light bool) {
	lightTheme = light
	p := DarkPalette
	if light {
		p = LightPalette
	}
	ColBase = p.Base
	ColSidebar = p.Sidebar
	ColCard = p.Card
	ColCardHover = p.CardHover
	ColControl = p.Control
	ColControlHi = p.ControlHi
	ColDivider = p.Divider
	ColEdge = p.Edge
	ColText = p.Text
	ColMuted = p.Muted
	ColFaint = p.Faint
	ColInk = p.Ink
	ColPrimaryHover = p.PrimaryHover
	ColLive = p.Live
}

// IsLight reports whether the light theme is active.
func IsLight() bool { return lightTheme }

func themeTag() string {
	if lightTheme {
		return "l"
	}
	return "d"
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 0xFF} }
