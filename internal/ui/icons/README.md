# Icons

[Tabler Icons](https://tabler.io/icons) 3.49.0 by Paweł Kuna, MIT ([LICENSE](LICENSE)), unmodified: outline icons under their own name, filled ones with `-filled`. They are embedded into the exe (`go:embed icons/*.svg` in `../icon.go`); nothing is loaded at run time.

Add one with `.\scripts\add-icon.ps1 <name>` (or `-Filled <name>`), then give it a `Glyph…` constant in `../widgets.go`. `go test ./internal/ui` parses every file here and checks the constants name existing files; `FLIT_RENDER_DIR=<folder> go test ./internal/ui -run IconPicture` draws them all at 14, 18 and 24 px.

How they are drawn: `icon.go` reads each file's `<path>` (and `circle`, `line`, `polyline`, `polygon`) elements, flattens curves and arcs, and outlines the 2-unit strokes with round caps and joins into polygons, which Direct2D fills in the colour of the widget (hover, disabled, ink on white). Filled shapes are filled as they are. Gradients and several colours aren't supported: an icon has one colour.
