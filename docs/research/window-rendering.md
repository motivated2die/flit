# What a frame of the window costs

Question (2026-11-22): the window is rebuilt from the app's state and drawn whole on every repaint (`internal/ui/view.go`, painted by Direct2D's software DC render target in `d2d_windows.go`). Can that carry animations that repaint at the display's pace, such as smooth wheel scrolling, or would they need a different rendering model (cached layers, scrolling by blitting)?

## Measurement

A throw-away test laid out and painted a `Frame` with a long recording page: a title and 400 transcript paragraphs of about 40 words each, the docked player at the bottom, scrolled 40 px further on each of 60 frames. Timed per frame, on the author's PC (Windows 11, 2026-11-22), without the final `BitBlt` to the window:

| Scale | Surface | Layout | Paint |
|---|---|---|---|
| 100% | 960×660 | 0.21 ms | 3.1 ms |
| 150% | 1440×990 | 0.20 ms | 3.9 ms |

Layout stays small because off-screen paragraphs are only measured, not drawn (`Ctx.Visible`), and DirectWrite's paragraph heights are cached per text and width (`d2d.ParagraphHeight`). Painting is dominated by the visible text.

## Consequence

About 4 ms per frame against a frame budget of ~15.6 ms (the system timer's tick, which `SetTimer` rounds up to): smooth scrolling fits the current model, with no layer caching. The host eases each wheel step towards its target with a 50 ms time constant on a 10 ms timer (in practice ~64 Hz), stops the timer as soon as the target is reached, and the Frame rounds the offset to whole pixels so text isn't drawn between pixels while it moves (`host.scrollSmoothly`, `stepScroll` in `internal/ui/host_windows.go`). Nothing repaints while nothing moves.

Unverified: the cost on a slow machine (a low-end laptop at 200%); the measurement above is one PC. If it ever shows, the first lever is drawing the page once into a taller bitmap and blitting the visible part while scrolling.

## Live regions

Question (2026-11-22): the voice meter's bars should move smoothly (about 60 frames a second) while recording. A full frame is 3–4 ms; 60 of them a second would be a fifth of a core. Measured on the same PC, per frame of the meter's bars (two groups of 9 bars, in a 960×660 surface), with throw-away tests:

| How | 100% | 150% |
|---|---|---|
| Direct2D bound to the whole surface, clipped to the bars | 1.2 ms | 2.4 ms |
| Direct2D bound to each bar group only (BindDC on a sub-rectangle, shifted) | 0.54 ms | 0.58 ms |
| Direct2D bound once to the meter's middle, labels redrawn too | 0.45 ms | 0.46 ms |
| of which: BindDC + BeginDraw + EndDraw with nothing drawn | 0.23 ms | |
| From Go into the surface's pixels (`pixelCanvas`, anti-aliased by distance) | 0.044 ms | 0.10 ms |

Direct2D's DC render target copies everything it is bound to on EndDraw, so its cost grows with the bound area, and a pass has a fixed cost of about a quarter of a millisecond. For a few axis-aligned rounded bars, drawing them in Go is ten times cheaper. `TestLiveRepaintMatches` (`internal/ui/pixelcanvas_windows_test.go`) checks that the Go-drawn bars look like Direct2D's (mean difference under 3 of 255 per channel) and that a frame stays under 1 ms (a margin for slower machines; Direct2D would be 0.45 ms).

So: `Ctx.DrawLive` registers a region that draws only shapes; the host repaints just those regions on a ~64 Hz timer (`timerLive`, the system timer's tick) straight into the kept surface and blits them to the window, with no layout pass (`host.paintLive`). The full paint (20 fps while something live is on screen) stays as it was. At 64 Hz the meter costs about 3–6 ms per second, 0.3–0.6% of one core, only while it is on screen during a recording. The audio side, a 512-point FFT per track per 32 ms, takes 7 µs a window (`BenchmarkSpectrumWindow`), 0.05% of a core for both tracks.
