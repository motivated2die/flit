# 0005: Drawn caption buttons, native frame

- **Status:** accepted
- **Date:** 2026-11-21

## Context

The main window had Windows' own title bar, tinted through DWM (dark mode, caption and border colours). The author asked for flit's own minimise and close buttons (close turning flit's red on hover), as long as this neither makes the window less stable nor adds much complexity.

## Options

1. **Keep the native title bar.** No work, no risk; the caption buttons look like every other app's, and the strip above the sidebar and page is a separate grey band.
2. **Borderless window (`WS_POPUP`) and everything drawn.** Full control, but resizing, snapping, the system menu, maximise placement, the Windows 11 border and shadow, and accessibility of the frame would all have to be rebuilt. Rejected: much code, many ways to get it subtly wrong.
3. **Native frame, caption folded into the client area.** The window keeps `WS_OVERLAPPEDWINDOW`; `WM_NCCALCSIZE` hands the caption's height to the client area (the side and bottom frame stay native, and a maximised window keeps clear of the frame it overhangs the screen by); `WM_NCHITTEST` reports the top frame band as `HTTOP`/corners and the top 32 px as `HTCAPTION` wherever nothing clickable is drawn. The Frame draws minimise and close (46×32, Segoe MDL2 glyphs).

## Decision

Option 3. It is about 80 lines (`internal/ui/window_windows.go`, `frame.go`) and keeps everything Windows does for a frame: resize on all edges, Aero Snap, double-click to maximise, drag, right-click and Alt+Space system menu, the taskbar, the Windows 11 rounded border and shadow.

## Consequences

- Checked: `TestWindowCaption` creates the real window and checks the client starts at the window's top edge and what each region hit-tests as; on screen at 125% the client fills the work area exactly when maximised (2560×1380 on a 2560×1440 screen with the taskbar).
- No maximise button is drawn (the window is small by nature); maximise stays available by double-click, Win+↑ and dragging to the top.
- Windows 11's snap-layout flyout (shown when hovering a maximise button) is not offered.
- Windows 10 draws no 1 px border along the top edge (the client covers it). Not drawn by flit for now.
- If this ever misbehaves, removing the two message cases and `Frame.Caption` restores the native title bar.
