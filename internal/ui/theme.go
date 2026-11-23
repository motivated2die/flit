// Package ui draws flit's surfaces: the main window, the notch, the tray
// icon. Drawing logic is portable and tested; the Windows parts live in
// _windows.go files.
package ui

import "image/color"

// flit's palette: neutral dark greys, clear white and grey text, and one
// accent, a berry-tinted red, used only where something is live (recording).
// Every surface draws with these tokens, never with literal colours.
var (
	ColBase      = rgb(0x14, 0x14, 0x14) // window background, notch
	ColSidebar   = rgb(0x19, 0x19, 0x19) // navigation column, title bar
	ColCard      = rgb(0x1E, 0x1E, 0x1E) // grouped content
	ColCardHover = rgb(0x24, 0x24, 0x24)
	ColControl   = rgb(0x2C, 0x2C, 0x2C) // buttons, tracks, unselected controls
	ColControlHi = rgb(0x36, 0x36, 0x36) // hovered controls
	ColDivider   = rgb(0x27, 0x27, 0x27)
	ColEdge      = rgb(0x2D, 0x2D, 0x2D) // outlines of floating surfaces

	ColText  = rgb(0xF4, 0xF4, 0xF4) // primary text, the "on" state of controls
	ColMuted = rgb(0xA3, 0xA3, 0xA3) // secondary text
	ColFaint = rgb(0x70, 0x70, 0x70) // captions, disabled
	ColInk   = rgb(0x14, 0x14, 0x14) // text on white (primary buttons)

	// ColLive is the only accent: recording, live.
	ColLive = rgb(0xF0, 0x3C, 0x52)

	// For a light taskbar: the tray icon's ink.
	ColInkOnLight = rgb(0x1C, 0x1C, 0x1C)
)

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 0xFF} }
