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

About 4 ms per frame against a 60 Hz frame budget of 16.6 ms (or 6.9 ms at 144 Hz): smooth scrolling fits the current model, with no layer caching. Originally the host drove smooth scrolling, transitions and live regions with `SetTimer`, which Windows quantizes to the ~15.625 ms system clock tick (~64 Hz) and leaves unsynchronized with DWM VSync — causing visible stutter/judder on 120–240 Hz displays.

**Update (2026-11-24 — DWM VSync pacing):** Smooth scrolling, short UI transitions (dock slide, modal fade via `Ctx.AnimatingFast`), window fade transitions (`transition_windows.go`), live regions (`DrawLive`), and edge-selection scrolling are now paced by `vsyncPacer` (`internal/ui/vsync_windows.go`), which blocks a lightweight goroutine on `DwmFlush()` only while active and posts at most one in-flight `wmVsync` message per monitor refresh (`UpdateWindow` paints immediately on receipt). Idle windows have zero pacer goroutines. Precision touchpad / hi-res wheel events (`|delta| < 120`) bypass the 50 ms easing and apply directly for 1:1 tracking. Low-frequency ambient animations (pulsing recording dot, blinking caret, playback clock) remain on `timerAnimate` (20 fps).

Unverified: the cost on a slow machine (a low-end laptop at 200%) or maximized 4K window; the measurement above is one PC at 960×660. If it ever shows, the next levers are culling off-screen widgets (`Ctx.Visible`) and scrolling by blitting the overlapping portion of the kept surface.

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
