package ui

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Direct2D and DirectWrite draw flit's layered surfaces (the notch and its
// popovers): anti-aliased shapes and text on a transparent background. They
// are COM interfaces, called through their vtables without cgo.
//
// The x64 calling convention passes the first four arguments in registers,
// floats in XMM registers, which syscall.SyscallN can't fill. So only calls
// whose floats come by pointer or after the fourth argument (on the stack,
// where float bits passed as integers land correctly) are used: strokes are
// drawn as filled paths, opacity is the colour's alpha.
//
// Drawing goes to a surface: a top-down 32-bit DIB with premultiplied
// alpha, as UpdateLayeredWindow takes it, through a software DC render
// target at 96 DPI (one DIP is one pixel; callers scale).

var (
	d2d1DLL               = windows.NewLazySystemDLL("d2d1.dll")
	dwriteDLL             = windows.NewLazySystemDLL("dwrite.dll")
	procD2D1CreateFactory = d2d1DLL.NewProc("D2D1CreateFactory")
	procDWriteCreate      = dwriteDLL.NewProc("DWriteCreateFactory")
	procCreateCompatDC    = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC          = gdi32.NewProc("DeleteDC")
	procSelectObject      = gdi32.NewProc("SelectObject")
	procGdiFlush          = gdi32.NewProc("GdiFlush")

	iidD2D1Factory   = windows.GUID{Data1: 0x06152247, Data2: 0x6f50, Data3: 0x465a, Data4: [8]byte{0x92, 0x45, 0x11, 0x8b, 0xfd, 0x3b, 0x60, 0x07}}
	iidDWriteFactory = windows.GUID{Data1: 0xb859ee5a, Data2: 0xd838, Data3: 0x4b5b, Data4: [8]byte{0xa2, 0xe8, 0x1a, 0xdc, 0x7d, 0x93, 0xdb, 0x48}}
)

// Vtable indices (IUnknown takes 0–2), from d2d1.h and dwrite.h.
const (
	factoryCreatePathGeometry  = 10
	factoryCreateHwndRenderTgt = 14
	factoryCreateDCRenderTgt   = 16
	rtCreateBitmap             = 4
	rtCreateBitmapBrush        = 7
	rtCreateSolidColorBrush    = 8
	rtCreateCompatRenderTarget = 12
	rtFillRectangle            = 17
	rtFillRoundedRectangle     = 19
	rtFillEllipse              = 21
	rtFillGeometry             = 23
	rtDrawText                 = 27
	rtDrawTextLayout           = 28
	tlSetUnderline             = 36
	tlSetDrawingEffect         = 38
	rtPushAxisAlignedClip      = 45
	rtPopAxisAlignedClip       = 46
	sinkAddLines               = 6
	rtSetTextAntialiasMode     = 34
	rtClear                    = 47
	rtBeginDraw                = 48
	rtEndDraw                  = 49
	dcrtBindDC                 = 57
	bmprtGetBitmap             = 57
	hwndrtResize               = 58
	brushSetColor              = 8
	pathOpen                   = 17
	sinkSetFillMode            = 3
	sinkBeginFigure            = 5
	sinkEndFigure              = 8
	sinkClose                  = 9
	sinkAddLine                = 10
	sinkAddArc                 = 14
	dwCreateTextFormat         = 15
	dwCreateTextLayout         = 18
	tfSetTextAlignment         = 3
	tfSetParagraphAlignment    = 4
	tfSetWordWrapping          = 5
	tlGetLineMetrics           = 59
	tlGetMetrics               = 60
	tlGetClusterMetrics        = 62
	d2dTextAntialiasGrayscale  = 2
	d2dRenderTargetDefault     = 0
	d2dRenderTargetSoftware    = 1
	dxgiFormatB8G8R8A8UNorm    = 87
	d2dAlphaModePremultiplied  = 1
	d2dAlphaModeIgnore         = 3
	d2dPresentImmediately      = 2
	d2dErrRecreateTarget       = 0x8899000C
	dwriteFontStyleNormal      = 0
	dwriteFontStretchNormal    = 5
	dwriteParagraphAlignCenter = 2
	dwriteNoWrap               = 1
)

// ErrRecreateTarget is returned by Painter.End when Direct2D signals device
// loss (D2DERR_RECREATE_TARGET) and the render target must be rebuilt.
var ErrRecreateTarget = errors.New("Direct2D target lost")

type comObj struct{ vtbl *[96]uintptr }

// call invokes vtable method i. uintptrescapes keeps addresses of Go
// values passed as arguments alive and in place during the call.
//
//go:uintptrescapes
func (o *comObj) call(i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[i], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObj) release() {
	if o != nil {
		o.call(2)
	}
}

func hr(r uintptr, what string) error {
	if int32(r) < 0 {
		return fmt.Errorf("%s: HRESULT %#x", what, uint32(r))
	}
	return nil
}

type d2d struct {
	factory, dwrite, rt, brush *comObj
	formats                    map[fmtKey]*comObj
	paraHeights                map[paraKey]float32 // ParagraphHeight results (UI thread only)
	bitmaps                    map[string]*comObj  // Canvas.Bitmap's pictures (UI thread only)
	paraLines                  map[paraKey]*TextLines
	styledLines                map[string]*TextLines // StyledLines results
}

var (
	d2dOnce sync.Once
	theD2D  *d2d
	d2dErr  error
)

// getD2D creates the factories and the render target once (UI thread).
func getD2D() (*d2d, error) {
	d2dOnce.Do(func() { theD2D, d2dErr = newD2D() })
	return theD2D, d2dErr
}

func newD2D() (*d2d, error) {
	if procD2D1CreateFactory.Find() != nil || procDWriteCreate.Find() != nil {
		return nil, errors.New("Direct2D isn't available")
	}
	g := &d2d{formats: map[fmtKey]*comObj{}}
	r, _, _ := procD2D1CreateFactory.Call(0, uintptr(unsafe.Pointer(&iidD2D1Factory)), 0, uintptr(unsafe.Pointer(&g.factory)))
	if err := hr(r, "D2D1CreateFactory"); err != nil {
		return nil, err
	}
	r, _, _ = procDWriteCreate.Call(0, uintptr(unsafe.Pointer(&iidDWriteFactory)), uintptr(unsafe.Pointer(&g.dwrite)))
	if err := hr(r, "DWriteCreateFactory"); err != nil {
		return nil, err
	}
	props := struct {
		Type        uint32
		Format      uint32
		AlphaMode   uint32
		DpiX, DpiY  float32
		Usage, MinL uint32
	}{Type: d2dRenderTargetSoftware, Format: dxgiFormatB8G8R8A8UNorm, AlphaMode: d2dAlphaModePremultiplied, DpiX: 96, DpiY: 96}
	if err := hr(g.factory.call(factoryCreateDCRenderTgt, uintptr(unsafe.Pointer(&props)), uintptr(unsafe.Pointer(&g.rt))), "CreateDCRenderTarget"); err != nil {
		return nil, err
	}
	g.rt.call(rtSetTextAntialiasMode, d2dTextAntialiasGrayscale) // ClearType needs an opaque background
	black := ColorF{0, 0, 0, 1}
	if err := hr(g.rt.call(rtCreateSolidColorBrush, uintptr(unsafe.Pointer(&black)), 0, uintptr(unsafe.Pointer(&g.brush))), "CreateSolidColorBrush"); err != nil {
		return nil, err
	}
	return g, nil
}

func f32(v float32) uintptr { return uintptr(math.Float32bits(v)) }

// pt packs a D2D1_POINT_2F, which (8 bytes) travels in one integer register.
func pt(x, y float32) uintptr { return uintptr(math.Float32bits(x)) | uintptr(math.Float32bits(y))<<32 }

// fmtKey selects a cached text format: a font, and whether it wraps (a
// paragraph, top-aligned) or not (a label, centred vertically).
type fmtKey struct {
	Font
	wrap bool
}

func (g *d2d) format(f Font) (*comObj, error) { return g.formatFor(fmtKey{Font: f}) }

func (g *d2d) formatFor(k fmtKey) (*comObj, error) {
	f := k.Font
	if tf := g.formats[k]; tf != nil {
		return tf, nil
	}
	family, _ := windows.UTF16PtrFromString(f.Family)
	locale, _ := windows.UTF16PtrFromString("en-us")
	var tf *comObj
	// fontSize is the 6th argument (with this): on the stack, so its bits
	// may be passed as an integer.
	if err := hr(g.dwrite.call(dwCreateTextFormat, uintptr(unsafe.Pointer(family)), 0, uintptr(f.Weight),
		dwriteFontStyleNormal, dwriteFontStretchNormal, f32(f.Size), uintptr(unsafe.Pointer(locale)), uintptr(unsafe.Pointer(&tf))), "CreateTextFormat"); err != nil {
		return nil, err
	}
	switch {
	case f.Center:
		tf.call(tfSetTextAlignment, 2)
	case f.Right:
		tf.call(tfSetTextAlignment, 1)
	}
	if k.wrap {
		tf.call(tfSetParagraphAlignment, 0) // near: top
	} else {
		tf.call(tfSetParagraphAlignment, dwriteParagraphAlignCenter)
		tf.call(tfSetWordWrapping, dwriteNoWrap)
	}
	g.formats[k] = tf
	return tf, nil
}

// ParagraphHeight returns the height of s wrapped to width in f.
func (g *d2d) ParagraphHeight(s string, f Font, width float32) float32 {
	k := paraKey{s, f, width}
	if h, ok := g.paraHeights[k]; ok {
		return h
	}
	h := g.paragraphHeight(s, f, width)
	if g.paraHeights == nil || len(g.paraHeights) > 8192 {
		g.paraHeights = map[paraKey]float32{}
	}
	g.paraHeights[k] = h
	return h
}

type paraKey struct {
	s     string
	f     Font
	width float32
}

func (g *d2d) paragraphHeight(s string, f Font, width float32) float32 {
	tf, err := g.formatFor(fmtKey{Font: f, wrap: true})
	if err != nil || s == "" {
		return 0
	}
	u := utf16Units(s)
	var tl *comObj
	if hr(g.dwrite.call(dwCreateTextLayout, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(tf)),
		f32(width), f32(1e6), uintptr(unsafe.Pointer(&tl))), "CreateTextLayout") != nil {
		return 0
	}
	defer tl.release()
	var m struct {
		Left, Top, Width, WidthTrailing, Height, LayoutW, LayoutH float32
		MaxBidi, Lines                                            uint32
	}
	tl.call(tlGetMetrics, uintptr(unsafe.Pointer(&m)))
	return m.Height
}

// ParagraphLines lays s out wrapped to width in f, as Paragraph draws it,
// and returns its lines and caret stops (for selecting text): from
// DirectWrite's line and cluster metrics, which take only pointers and
// integers (its hit-testing calls take floats in registers, which these
// cgo-free calls can't pass).
func (g *d2d) ParagraphLines(s string, f Font, width float32) *TextLines {
	k := paraKey{s, f, width}
	if t, ok := g.paraLines[k]; ok {
		return t
	}
	t := g.paragraphLines(s, f, width)
	if g.paraLines == nil || len(g.paraLines) > 4096 {
		g.paraLines = map[paraKey]*TextLines{}
	}
	g.paraLines[k] = t
	return t
}

func (g *d2d) paragraphLines(s string, f Font, width float32) *TextLines {
	tf, err := g.formatFor(fmtKey{Font: f, wrap: true})
	if err != nil || s == "" {
		return &TextLines{}
	}
	u := utf16Units(s)
	var tl *comObj
	if hr(g.dwrite.call(dwCreateTextLayout, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(tf)),
		f32(width), f32(1e6), uintptr(unsafe.Pointer(&tl))), "CreateTextLayout") != nil {
		return &TextLines{}
	}
	defer tl.release()
	return layoutLines(tl, s, len(u)-1)
}

// layoutLines reads a text layout's lines and caret stops (s is its text,
// n its length in UTF-16 units).
func layoutLines(tl *comObj, s string, units int) *TextLines {
	type lineMetrics struct {
		Length, TrailingWS, NewlineLen uint32
		Height, Baseline               float32
		IsTrimmed                      int32
	}
	type clusterMetrics struct {
		Width  float32
		Length uint16
		Flags  uint16
	}
	lines := make([]lineMetrics, 64)
	var n uint32
	if r := tl.call(tlGetLineMetrics, uintptr(unsafe.Pointer(&lines[0])), uintptr(len(lines)), uintptr(unsafe.Pointer(&n))); int32(r) < 0 && int(n) > len(lines) {
		lines = make([]lineMetrics, n) // E_NOT_SUFFICIENT_BUFFER: n says how many
		tl.call(tlGetLineMetrics, uintptr(unsafe.Pointer(&lines[0])), uintptr(len(lines)), uintptr(unsafe.Pointer(&n)))
	}
	lines = lines[:min(int(n), len(lines))]
	clusters := make([]clusterMetrics, max(1, units))
	var m uint32
	tl.call(tlGetClusterMetrics, uintptr(unsafe.Pointer(&clusters[0])), uintptr(len(clusters)), uintptr(unsafe.Pointer(&m)))
	clusters = clusters[:min(int(m), len(clusters))]
	var ll, cl []int
	var lh, cw []float32
	for _, l := range lines {
		ll, lh = append(ll, int(l.Length)), append(lh, l.Height)
	}
	for _, c := range clusters {
		cl, cw = append(cl, int(c.Length)), append(cw, c.Width)
	}
	return linesFromMetrics(s, ll, lh, cl, cw)
}

// TextWidth and ParagraphHeight make the Direct2D context a Metrics.
func (g *d2d) TextWidth(s string, f Font) float32 { return g.Measure(s, f) }

// D2DMetrics returns text measurements by DirectWrite.
func D2DMetrics() (Metrics, error) {
	g, err := getD2D()
	if err != nil {
		return nil, err
	}
	return g, nil
}

// Measure returns the width of s in f, in pixels.
func (g *d2d) Measure(s string, f Font) float32 {
	tf, err := g.format(f)
	if err != nil || s == "" {
		return 0
	}
	u := utf16Units(s)
	var tl *comObj
	if hr(g.dwrite.call(dwCreateTextLayout, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(tf)),
		f32(10000), f32(1000), uintptr(unsafe.Pointer(&tl))), "CreateTextLayout") != nil {
		return 0
	}
	defer tl.release()
	var m struct {
		Left, Top, Width, WidthTrailing, Height, LayoutW, LayoutH float32
		MaxBidi, Lines                                            uint32
	}
	tl.call(tlGetMetrics, uintptr(unsafe.Pointer(&m)))
	return m.WidthTrailing
}

// Surface is a premultiplied BGRA bitmap with a memory DC.
type Surface struct {
	W, H int32
	dc   uintptr
	bmp  uintptr
	old  uintptr
	Bits []byte
}

// NewSurface allocates a w×h surface.
func NewSurface(w, h int32) (*Surface, error) {
	bi := bitmapInfoHeader{Width: w, Height: -h, Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return nil, errors.New("CreateDIBSection failed")
	}
	dc, _, _ := procCreateCompatDC.Call(0)
	old, _, _ := procSelectObject.Call(dc, bmp)
	return &Surface{W: w, H: h, dc: dc, bmp: bmp, old: old, Bits: unsafe.Slice((*byte)(bits), int(w*h*4))}, nil
}

// Free releases it.
func (s *Surface) Free() {
	if s == nil || s.dc == 0 {
		return
	}
	procSelectObject.Call(s.dc, s.old)
	procDeleteObject.Call(s.bmp)
	procDeleteDC.Call(s.dc)
	s.dc = 0
}

// HwndTarget owns a hardware-accelerated ID2D1HwndRenderTarget (with automatic
// WARP fallback) and its device-dependent resources (brush, cached bitmaps)
// for one opaque Win32 window. UI thread only.
type HwndTarget struct {
	hwnd    uintptr
	w, h    uint32
	rt      *comObj
	brush   *comObj
	bitmaps map[string]*comObj
}

// ready reports whether the render target has been created.
func (t *HwndTarget) ready() bool { return t != nil && t.rt != nil }

// Free releases the render target and all device-dependent resources.
func (t *HwndTarget) Free() {
	if t == nil {
		return
	}
	for k, b := range t.bitmaps {
		b.release()
		delete(t.bitmaps, k)
	}
	t.brush.release()
	t.brush = nil
	t.rt.release()
	t.rt = nil
	t.w, t.h = 0, 0
}

// Begin creates or resizes the hardware render target for (w, h) device pixels,
// starts drawing, and clears to bg.
func (t *HwndTarget) Begin(hwnd uintptr, w, h int32, bg ColorF) (*Painter, error) {
	if hwnd == 0 || w <= 0 || h <= 0 {
		return nil, errors.New("invalid window target size")
	}
	g, err := getD2D()
	if err != nil {
		return nil, err
	}
	uw, uh := uint32(w), uint32(h)
	if t.rt != nil && t.hwnd != hwnd {
		t.Free()
	}
	if t.rt == nil {
		props := struct {
			Type        uint32
			Format      uint32
			AlphaMode   uint32
			DpiX, DpiY  float32
			Usage, MinL uint32
		}{
			Type:      d2dRenderTargetDefault,
			Format:    dxgiFormatB8G8R8A8UNorm,
			AlphaMode: d2dAlphaModeIgnore,
			DpiX:      96,
			DpiY:      96,
		}
		hwndProps := struct {
			Hwnd           uintptr
			Width, Height  uint32
			PresentOptions uint32
		}{
			Hwnd:           hwnd,
			Width:          uw,
			Height:         uh,
			PresentOptions: d2dPresentImmediately,
		}
		var rt *comObj
		if err := hr(g.factory.call(factoryCreateHwndRenderTgt,
			uintptr(unsafe.Pointer(&props)),
			uintptr(unsafe.Pointer(&hwndProps)),
			uintptr(unsafe.Pointer(&rt))), "CreateHwndRenderTarget"); err != nil {
			return nil, err
		}
		rt.call(rtSetTextAntialiasMode, d2dTextAntialiasGrayscale)
		black := ColorF{0, 0, 0, 1}
		var brush *comObj
		if err := hr(rt.call(rtCreateSolidColorBrush, uintptr(unsafe.Pointer(&black)), 0, uintptr(unsafe.Pointer(&brush))), "CreateSolidColorBrush"); err != nil {
			rt.release()
			return nil, err
		}
		t.hwnd, t.rt, t.brush, t.w, t.h = hwnd, rt, brush, uw, uh
	} else if t.w != uw || t.h != uh {
		sz := struct{ W, H uint32 }{uw, uh}
		r := t.rt.call(hwndrtResize, uintptr(unsafe.Pointer(&sz)))
		if uint32(r) == d2dErrRecreateTarget {
			t.Free()
			return nil, ErrRecreateTarget
		}
		if err := hr(r, "Resize"); err != nil {
			return nil, err
		}
		t.w, t.h = uw, uh
	}
	t.rt.call(rtBeginDraw)
	t.rt.call(rtClear, uintptr(unsafe.Pointer(&bg)))
	return &Painter{g: g, rt: t.rt, brushObj: t.brush, bitmaps: &t.bitmaps}, nil
}

// CaptureBrush renders draw onto a GPU-backed offscreen ID2D1BitmapRenderTarget
// matching t's current size and returns an ID2D1BitmapBrush for compositing
// (used for the circular theme ripple transition). Caller must release the
// returned brush.
func (t *HwndTarget) CaptureBrush(draw func(*Painter), bg ColorF) (*comObj, error) {
	if !t.ready() {
		return nil, errors.New("hardware render target not ready")
	}
	g, err := getD2D()
	if err != nil {
		return nil, err
	}
	var compat *comObj
	if err := hr(t.rt.call(rtCreateCompatRenderTarget, 0, 0, 0, 0, uintptr(unsafe.Pointer(&compat))), "CreateCompatibleRenderTarget"); err != nil {
		return nil, err
	}
	defer compat.release()

	compat.call(rtSetTextAntialiasMode, d2dTextAntialiasGrayscale)
	black := ColorF{0, 0, 0, 1}
	var solid *comObj
	if err := hr(compat.call(rtCreateSolidColorBrush, uintptr(unsafe.Pointer(&black)), 0, uintptr(unsafe.Pointer(&solid))), "CreateSolidColorBrush"); err != nil {
		return nil, err
	}
	defer solid.release()

	var bmps map[string]*comObj
	defer func() {
		for _, b := range bmps {
			b.release()
		}
	}()

	compat.call(rtBeginDraw)
	compat.call(rtClear, uintptr(unsafe.Pointer(&bg)))
	p := &Painter{g: g, rt: compat, brushObj: solid, bitmaps: &bmps}
	draw(p)
	if err := p.End(); err != nil {
		return nil, err
	}

	var bmp *comObj
	if err := hr(compat.call(bmprtGetBitmap, uintptr(unsafe.Pointer(&bmp))), "GetBitmap"); err != nil {
		return nil, err
	}
	defer bmp.release()

	bbp := struct{ ExtendX, ExtendY, Interp uint32 }{0, 0, 0} // clamp, nearest 1:1
	bp := struct {
		Opacity float32
		M       [6]float32
	}{1, [6]float32{1, 0, 0, 1, 0, 0}}
	var brush *comObj
	if err := hr(t.rt.call(rtCreateBitmapBrush, uintptr(unsafe.Pointer(bmp)), uintptr(unsafe.Pointer(&bbp)), uintptr(unsafe.Pointer(&bp)), uintptr(unsafe.Pointer(&brush))), "CreateBitmapBrush"); err != nil {
		return nil, err
	}
	return brush, nil
}

// FillBrush fills r with a pre-created Direct2D brush (e.g. a snapshot bitmap brush).
func (p *Painter) FillBrush(r RectF, brush *comObj) {
	if brush != nil {
		p.rt.call(rtFillRectangle, uintptr(unsafe.Pointer(&r)), uintptr(unsafe.Pointer(brush)))
	}
}

// FillEllipseBrush fills a circle/ellipse with a pre-created Direct2D brush.
func (p *Painter) FillEllipseBrush(cx, cy, rx, ry float32, brush *comObj) {
	if brush != nil && rx > 0 && ry > 0 {
		e := struct{ X, Y, RX, RY float32 }{cx, cy, rx, ry}
		p.rt.call(rtFillEllipse, uintptr(unsafe.Pointer(&e)), uintptr(unsafe.Pointer(brush)))
	}
}

// Painter draws on a render target (either a software Surface or a hardware
// HwndTarget) between Begin and End.
type Painter struct {
	g        *d2d
	rt       *comObj
	brushObj *comObj
	bitmaps  *map[string]*comObj
}

// Begin binds the software render target to s and clears it to transparent.
func Begin(s *Surface) (*Painter, error) { return BeginWith(s, ColorF{}) }

// BeginWith binds the software render target to s and clears it to bg.
func BeginWith(s *Surface, bg ColorF) (*Painter, error) {
	g, err := getD2D()
	if err != nil {
		return nil, err
	}
	r := struct{ L, T, R, B int32 }{0, 0, s.W, s.H}
	if err := hr(g.rt.call(dcrtBindDC, s.dc, uintptr(unsafe.Pointer(&r))), "BindDC"); err != nil {
		return nil, err
	}
	g.rt.call(rtBeginDraw)
	g.rt.call(rtClear, uintptr(unsafe.Pointer(&bg)))
	return &Painter{g: g, rt: g.rt, brushObj: g.brush, bitmaps: &g.bitmaps}, nil
}

// End finishes drawing. Returns ErrRecreateTarget on device loss.
func (p *Painter) End() error {
	r := p.rt.call(rtEndDraw, 0, 0)
	if uint32(r) == d2dErrRecreateTarget {
		return ErrRecreateTarget
	}
	return hr(r, "EndDraw")
}

func (p *Painter) brush(c ColorF) uintptr {
	p.brushObj.call(brushSetColor, uintptr(unsafe.Pointer(&c)))
	return uintptr(unsafe.Pointer(p.brushObj))
}

// RoundRect fills a rounded rectangle.
func (p *Painter) RoundRect(r RectF, radius float32, c ColorF) {
	rr := struct {
		RectF
		RX, RY float32
	}{r, radius, radius}
	p.rt.call(rtFillRoundedRectangle, uintptr(unsafe.Pointer(&rr)), p.brush(c))
}

// Circle fills a circle.
func (p *Painter) Circle(cx, cy, r float32, c ColorF) {
	e := struct{ X, Y, RX, RY float32 }{cx, cy, r, r}
	p.rt.call(rtFillEllipse, uintptr(unsafe.Pointer(&e)), p.brush(c))
}

// Text draws s in r.
func (p *Painter) Text(s string, r RectF, f Font, c ColorF) {
	tf, err := p.g.format(f)
	if err != nil || s == "" {
		return
	}
	u := utf16Units(s)
	p.rt.call(rtDrawText, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(tf)),
		uintptr(unsafe.Pointer(&r)), p.brush(c), 2 /* clip */, 0)
}

// Paragraph draws s wrapped to r's width, from r's top.
func (p *Painter) Paragraph(s string, r RectF, f Font, c ColorF) {
	tf, err := p.g.formatFor(fmtKey{Font: f, wrap: true})
	if err != nil || s == "" {
		return
	}
	u := utf16Units(s)
	p.rt.call(rtDrawText, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(tf)),
		uintptr(unsafe.Pointer(&r)), p.brush(c), 0, 0)
}

// MarkedParagraph draws s wrapped like Paragraph, with marked parts in
// their own colours: a DirectWrite text layout with a brush (and an
// underline) per range, drawn with DrawTextLayout.
func (p *Painter) MarkedParagraph(s string, r RectF, f Font, c ColorF, marks []TextMark) {
	if len(marks) == 0 {
		p.Paragraph(s, r, f, c)
		return
	}
	tf, err := p.g.formatFor(fmtKey{Font: f, wrap: true})
	if err != nil || s == "" {
		return
	}
	u := utf16Units(s)
	var tl *comObj
	if hr(p.g.dwrite.call(dwCreateTextLayout, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(tf)),
		f32(r.W()), f32(max(r.H(), 1)), uintptr(unsafe.Pointer(&tl))), "CreateTextLayout") != nil {
		return
	}
	defer tl.release()
	for _, m := range marks {
		from, to := utf16Index(s, m.From), utf16Index(s, m.To)
		if to <= from {
			continue
		}
		rng := uintptr(uint32(from)) | uintptr(uint32(to-from))<<32 // DWRITE_TEXT_RANGE, by value
		var b *comObj
		col := m.Color
		if hr(p.rt.call(rtCreateSolidColorBrush, uintptr(unsafe.Pointer(&col)), 0, uintptr(unsafe.Pointer(&b))), "CreateSolidColorBrush") != nil {
			continue
		}
		tl.call(tlSetDrawingEffect, uintptr(unsafe.Pointer(b)), rng) // the layout keeps its own reference
		b.release()
		if m.Underline {
			tl.call(tlSetUnderline, 1, rng)
		}
	}
	p.rt.call(rtDrawTextLayout, pt(r.L, r.T), uintptr(unsafe.Pointer(tl)), p.brush(c), 0)
}

// utf16Units encodes s as UTF-16 code units with a trailing NUL (like
// windows.UTF16FromString, but never failing on embedded NUL bytes).
func utf16Units(s string) []uint16 {
	u := utf16.Encode([]rune(s))
	return append(u, 0)
}

// utf16Index is byte offset i of s in UTF-16 code units (DirectWrite's).
func utf16Index(s string, i int) int {
	n := 0
	for _, r := range s[:max(0, min(i, len(s)))] {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// PushClip limits drawing to r until PopClip.
func (p *Painter) PushClip(r RectF) {
	p.rt.call(rtPushAxisAlignedClip, uintptr(unsafe.Pointer(&r)), 0)
}

// PopClip ends the last PushClip.
func (p *Painter) PopClip() { p.rt.call(rtPopAxisAlignedClip) }

// Polygons fills closed polygons (non-zero winding).
func (p *Painter) Polygons(polys [][]PointF, c ColorF) {
	var path *comObj
	if hr(p.g.factory.call(factoryCreatePathGeometry, uintptr(unsafe.Pointer(&path))), "CreatePathGeometry") != nil {
		return
	}
	defer path.release()
	var sink *comObj
	if hr(path.call(pathOpen, uintptr(unsafe.Pointer(&sink))), "Open") != nil {
		return
	}
	sink.call(sinkSetFillMode, 1) // winding
	for _, poly := range polys {
		if len(poly) < 3 {
			continue
		}
		sink.call(sinkBeginFigure, pt(poly[0].X, poly[0].Y), 0)
		rest := poly[1:]
		sink.call(sinkAddLines, uintptr(unsafe.Pointer(&rest[0])), uintptr(len(rest)))
		sink.call(sinkEndFigure, 1)
	}
	sink.call(sinkClose)
	sink.release()
	p.rt.call(rtFillGeometry, uintptr(unsafe.Pointer(path)), p.brush(c), 0)
}

// NotchShape fills a top-attached notch blob spanning [l, r] × [t, b] with
// rounded bottom corners of radius bottomR and concave top flares of radius
// flareR curving smoothly outward along the top edge y = t.
func (p *Painter) NotchShape(l, t, r, b, bottomR, flareR float32, c ColorF) {
	w, h := r-l, b-t
	if w <= 0 || h <= 0 {
		return
	}
	br := max(0, min(bottomR, min(w/2, h)))
	fr := max(0, min(flareR, h-br))
	var path *comObj
	if hr(p.g.factory.call(factoryCreatePathGeometry, uintptr(unsafe.Pointer(&path))), "CreatePathGeometry") != nil {
		return
	}
	defer path.release()
	var sink *comObj
	if hr(path.call(pathOpen, uintptr(unsafe.Pointer(&sink))), "Open") != nil {
		return
	}
	addArc := func(x, y, rad float32, clockwise bool) {
		dir := uint32(0) // D2D1_SWEEP_DIRECTION_COUNTER_CLOCKWISE
		if clockwise {
			dir = 1
		}
		seg := struct {
			X, Y, W, H, Rot float32
			Dir, Size       uint32
		}{x, y, rad, rad, 0, dir, 0}
		sink.call(sinkAddArc, uintptr(unsafe.Pointer(&seg)))
	}
	sink.call(sinkSetFillMode, 1) // winding
	sink.call(sinkBeginFigure, pt(l-fr, t), 0)
	if fr > 0 {
		addArc(l, t+fr, fr, true) // top-left concave flare
	}
	if b-br > t+fr {
		sink.call(sinkAddLine, pt(l, b-br))
	}
	if br > 0 {
		addArc(l+br, b, br, false) // bottom-left convex corner
	}
	if r-br > l+br {
		sink.call(sinkAddLine, pt(r-br, b))
	}
	if br > 0 {
		addArc(r, b-br, br, false) // bottom-right convex corner
	}
	if b-br > t+fr {
		sink.call(sinkAddLine, pt(r, t+fr))
	}
	if fr > 0 {
		addArc(r+fr, t, fr, true) // top-right concave flare
	}
	sink.call(sinkEndFigure, 1) // closes along top edge y = t
	sink.call(sinkClose)
	sink.release()
	p.rt.call(rtFillGeometry, uintptr(unsafe.Pointer(path)), p.brush(c), 0)
}

// Arc fills a ring segment around (cx, cy) between radii inner and outer,
// clockwise from angle a0 to a1 (radians from twelve o'clock). A full turn
// draws a whole ring.
func (p *Painter) Arc(cx, cy, inner, outer, a0, a1 float32, c ColorF) {
	if a1-a0 >= 2*math.Pi-1e-3 {
		// Two half rings: one arc can't close on itself.
		p.Arc(cx, cy, inner, outer, a0, a0+math.Pi, c)
		p.Arc(cx, cy, inner, outer, a0+math.Pi, a0+2*math.Pi, c)
		return
	}
	if a1 <= a0 {
		return
	}
	var path *comObj
	if hr(p.g.factory.call(factoryCreatePathGeometry, uintptr(unsafe.Pointer(&path))), "CreatePathGeometry") != nil {
		return
	}
	defer path.release()
	var sink *comObj
	if hr(path.call(pathOpen, uintptr(unsafe.Pointer(&sink))), "Open") != nil {
		return
	}
	at := func(r, a float32) (float32, float32) {
		return cx + r*float32(math.Sin(float64(a))), cy - r*float32(math.Cos(float64(a)))
	}
	large := uint32(0)
	if a1-a0 > math.Pi {
		large = 1
	}
	arc := func(r, a float32, clockwise bool) {
		x, y := at(r, a)
		dir := uint32(0) // D2D1_SWEEP_DIRECTION_COUNTER_CLOCKWISE
		if clockwise {
			dir = 1
		}
		seg := struct {
			X, Y, W, H, Rot float32
			Dir, Size       uint32
		}{x, y, r, r, 0, dir, large}
		sink.call(sinkAddArc, uintptr(unsafe.Pointer(&seg)))
	}
	sink.call(sinkSetFillMode, 1) // winding
	sx, sy := at(outer, a0)
	sink.call(sinkBeginFigure, pt(sx, sy), 0) // filled
	arc(outer, a1, true)
	ix, iy := at(inner, a1)
	sink.call(sinkAddLine, pt(ix, iy))
	arc(inner, a0, false)
	sink.call(sinkEndFigure, 1) // closed
	sink.call(sinkClose)
	sink.release()
	p.rt.call(rtFillGeometry, uintptr(unsafe.Pointer(path)), p.brush(c), 0)
}

// Bitmap draws a cached premultiplied BGRA picture (see Canvas.Bitmap): a
// Direct2D bitmap made once per key, filled into its rectangle through a
// bitmap brush that carries the position and opacity.
func (p *Painter) Bitmap(key string, x, y float32, w, h int, opacity float32, pixels func() []byte) {
	p.drawBitmap(key, RectF{x, y, x + float32(w), y + float32(h)}, w, h, opacity, 0, pixels)
}

// ScaledBitmap draws a cached srcW×srcH premultiplied BGRA picture scaled into
// dst with hardware bilinear interpolation (D2D1_BITMAP_INTERPOLATION_MODE_LINEAR).
func (p *Painter) ScaledBitmap(key string, dst RectF, srcW, srcH int, opacity float32, pixels func() []byte) {
	p.drawBitmap(key, dst, srcW, srcH, opacity, 1, pixels)
}

func (p *Painter) drawBitmap(key string, dst RectF, srcW, srcH int, opacity float32, interp uint32, pixels func() []byte) {
	if srcW <= 0 || srcH <= 0 || dst.W() <= 0 || dst.H() <= 0 || opacity <= 0 {
		return
	}
	bmps := *p.bitmaps
	bmp := bmps[key]
	if bmp == nil {
		pix := pixels()
		if len(pix) < 4*srcW*srcH {
			return
		}
		if len(bmps) >= 16 { // sizes come and go with scales and panel heights
			for k, b := range bmps {
				b.release()
				delete(bmps, k)
			}
		}
		props := struct {
			Format, Alpha uint32
			DpiX, DpiY    float32
		}{dxgiFormatB8G8R8A8UNorm, d2dAlphaModePremultiplied, 96, 96}
		size := uintptr(uint32(srcW)) | uintptr(uint32(srcH))<<32 // D2D1_SIZE_U, by value in one register
		if hr(p.rt.call(rtCreateBitmap, size, uintptr(unsafe.Pointer(&pix[0])), uintptr(4*srcW), uintptr(unsafe.Pointer(&props)), uintptr(unsafe.Pointer(&bmp))), "CreateBitmap") != nil {
			return
		}
		if bmps == nil {
			bmps = map[string]*comObj{}
			*p.bitmaps = bmps
		}
		bmps[key] = bmp
	}
	bbp := struct{ ExtendX, ExtendY, Interp uint32 }{0, 0, interp} // clamp
	bp := struct {
		Opacity float32
		M       [6]float32
	}{opacity, [6]float32{dst.W() / float32(srcW), 0, 0, dst.H() / float32(srcH), dst.L, dst.T}}
	var brush *comObj
	if hr(p.rt.call(rtCreateBitmapBrush, uintptr(unsafe.Pointer(bmp)), uintptr(unsafe.Pointer(&bbp)), uintptr(unsafe.Pointer(&bp)), uintptr(unsafe.Pointer(&brush))), "CreateBitmapBrush") != nil {
		return
	}
	p.rt.call(rtFillRectangle, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(brush)))
	brush.release()
}

// Measure returns the width of s in f (render thread only).
func Measure(s string, f Font) float32 {
	g, err := getD2D()
	if err != nil {
		return 0
	}
	return g.Measure(s, f)
}
