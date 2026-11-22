# 0008: Tabler icons, outlined into Direct2D polygons by flit

- **Status:** accepted
- **Date:** 2026-11-22

## Context

The window and the tray panel drew their icons as text in Segoe MDL2 Assets, Windows' icon font: nothing to embed, crisp, but Windows' look rather than flit's, and only the glyphs that font has. The author asked for Tabler's icons everywhere (navigation, buttons, rows, caption buttons), as in Resound, which embeds Tabler's SVGs and rasterizes them with oksvg/rasterx into GDI alpha masks (`internal/ui/icon.go` there, [references/resound.md](../references/resound.md#icons)).

flit draws with Direct2D through hand-written vtable calls. Its calls can't pass floats in registers, so Direct2D's own stroking (`DrawGeometry`, whose stroke width is the fourth argument) isn't available (`internal/ui/d2d_windows.go`).

## Options

### A. Rasterize with oksvg/rasterx, as Resound does

A proven path, but two dependencies, and bitmaps per icon, size and colour: hover, pressed and disabled colours each need their own, drawn through `Canvas.Bitmap`'s small cache.

### B. Parse the SVGs and outline the strokes in flit's own code

Tabler's icons are paths with a 2-unit stroke and round caps and joins. Flatten curves and arcs, outline each segment as a rectangle with a disc at every end and sharp bend, all turned the same way so one non-zero fill draws their union without seams, and fill that with the existing `Canvas.Polygons`. About 580 lines plus 120 of tests, no dependency, any colour at no cost, cached per size.

## Decision

B. The SVG files are Tabler's own (3.49.0, MIT, unmodified, `internal/ui/icons/`), added with `scripts/add-icon.ps1`; the parser and outliner are flit's (`internal/ui/icon.go`).

## Consequences

- One look across window and panel; icons follow every widget colour; crisp at every scale (polygons, anti-aliased by Direct2D).
- The parser covers what Tabler uses: `path` (all commands, relative and absolute, arcs), `circle`, `line`, `polyline`, `polygon`; one colour per icon, no gradients. A new icon that uses something else fails `TestIconsParse`.
- Filled and stroked parts of one icon are filled together; a filled shape that runs the other way round than an overlapping stroke would cancel it. None of today's icons mixes the two.
- Strokes never get thinner than a pixel; at 14 px Tabler's strokes are about 1.2 px.

## Revisit when

- An icon needs several colours or a gradient.
- Drawing icons shows up in a frame's cost ([research/window-rendering.md](../research/window-rendering.md)); then cache them as bitmaps per colour.
