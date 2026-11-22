// Package assets holds files built into flit: the mark's source drawing
// and the app icon's artwork.
package assets

import _ "embed"

// FlitSVG is assets/flit.svg, the feather mark. internal/ui draws the tray
// icon and the window's mark from its path and gradient.
//
//go:embed flit.svg
var FlitSVG string

// LogoPNG is assets/logo.png (256×256), the application icon: the feather
// on a dark rounded tile. ui.AppIcon scales it to every size; flit.ico is
// generated from it (go run ./cmd/flitdev icon).
//
//go:embed logo.png
var LogoPNG []byte
