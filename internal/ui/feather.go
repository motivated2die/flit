package ui

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/motivated2die/flit/assets"
)

// The feather is flit's mark: a quill leaning right, its barbs notched into
// the outline. It is read from assets/flit.svg (built into the exe), so the
// drawing is the one source: its path is fitted into a unit square (y down)
// where it spans 0.09-0.91 in height, centred, and drawn by the rasterizer
// at any size (the tray icon, the window's mark). The app icon is
// assets/logo.png (appicon.go).

const (
	featherTop    = 0.09
	featherHeight = 0.82
)

// featherFit maps assets/flit.svg's coordinates into the unit square.
type featherFit struct{ k, dx, dy float64 }

func (f featherFit) at(x, y float64) (float64, float64) { return x*f.k + f.dx, y*f.k + f.dy }

// featherArt is what flit uses of the SVG.
type featherArt struct {
	path *Path
	fit  featherFit
	// The fill's linear gradient (SVG coordinates) and the opacity it ends
	// at; fade is 0 when the fill is flat.
	x1, y1, x2, y2, fade float64
}

var featherSrc = sync.OnceValue(func() featherArt {
	a, err := parseFeatherSVG(assets.FlitSVG)
	if err != nil {
		panic("assets/flit.svg: " + err.Error())
	}
	return a
})

func parseFeatherSVG(svg string) (featherArt, error) {
	var a featherArt
	m := regexp.MustCompile(`<path[^>]*\sd="([^"]+)"`).FindStringSubmatch(svg)
	if m == nil {
		return a, errSVG("no <path d=...>")
	}
	p, err := ParseSVGPath(m[1])
	if err != nil {
		return a, err
	}
	minX, minY, maxX, maxY := p.bounds()
	if !(maxY > minY) {
		return a, errSVG("empty path")
	}
	k := featherHeight / (maxY - minY)
	a.path, a.fit = p, featherFit{k, 0.5 - (minX+maxX)/2*k, featherTop - minY*k}
	if g := regexp.MustCompile(`<linearGradient[^>]*>`).FindString(svg); g != "" {
		attr := func(name string) float64 {
			m := regexp.MustCompile(`\s` + name + `="([-0-9.]+)"`).FindStringSubmatch(g)
			if m == nil {
				return 0
			}
			v, _ := strconv.ParseFloat(m[1], 64)
			return v
		}
		a.x1, a.y1, a.x2, a.y2 = attr("x1"), attr("y1"), attr("x2"), attr("y2")
		a.fade = 1
		stops := regexp.MustCompile(`<stop[^>]*>`).FindAllString(svg, -1)
		if n := len(stops); n > 0 {
			if m := regexp.MustCompile(`stop-opacity="([0-9.]+)"`).FindStringSubmatch(stops[n-1]); m != nil {
				a.fade, _ = strconv.ParseFloat(m[1], 64)
			}
		}
	}
	return a, nil
}

// feather is the mark in the unit square.
func feather() *Path {
	a := featherSrc()
	return a.path.Transform(a.fit.k, a.fit.dx, a.fit.dy)
}

// featherSpec places the feather in a size×size area at (ox, oy).
type featherSpec struct {
	size, ox, oy float64
}

func (f featherSpec) path() *Path { return feather().Transform(f.size, f.ox, f.oy) }

// gradient is assets/flit.svg's fill: opaque at the gradient's start
// fading to its last stop's opacity at its end. It returns the alpha factor
// at pixel (x, y) of a feather drawn at f (always 1 for a flat fill).
func (f featherSpec) gradient() func(x, y float64) float64 {
	a := featherSrc()
	if a.fade == 0 || a.fade == 1 {
		return func(x, y float64) float64 { return 1 }
	}
	px := func(x, y float64) (float64, float64) {
		ux, uy := a.fit.at(x, y)
		return ux*f.size + f.ox, uy*f.size + f.oy
	}
	ax, ay := px(a.x1, a.y1)
	bx, by := px(a.x2, a.y2)
	vx, vy := bx-ax, by-ay
	l2 := vx*vx + vy*vy
	return func(x, y float64) float64 {
		t := math.Max(0, math.Min(1, ((x-ax)*vx+(y-ay)*vy)/l2))
		return 1 - (1-a.fade)*t
	}
}

// bounds returns the path's bounding box.
func (p *Path) bounds() (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, poly := range append(p.polys, p.cur) {
		for _, v := range poly {
			minX, maxX = math.Min(minX, v.x), math.Max(maxX, v.x)
			minY, maxY = math.Min(minY, v.y), math.Max(maxY, v.y)
		}
	}
	return
}

// ParseSVGPath reads SVG path data with the commands M, L, H, V, C, Q and Z
// (absolute and relative), which is what icon editors export for outlines.
func ParseSVGPath(d string) (*Path, error) {
	toks := svgTokens(d)
	p := &Path{}
	var cmd byte
	var cx, cy, sx, sy float64
	i := 0
	num := func() (float64, error) {
		if i >= len(toks) {
			return 0, errSVG("missing number")
		}
		v, err := strconv.ParseFloat(toks[i], 64)
		if err != nil {
			return 0, errSVG("bad number " + toks[i])
		}
		i++
		return v, nil
	}
	nums := func(n int) ([]float64, error) {
		out := make([]float64, n)
		for k := range out {
			v, err := num()
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	}
	for i < len(toks) {
		if t := toks[i]; len(t) == 1 && strings.ContainsAny(t, "MmLlHhVvCcQqZz") {
			cmd = t[0]
			i++
		} else if cmd == 0 {
			return nil, errSVG("data before a command")
		}
		rel := cmd >= 'a'
		ox, oy := 0.0, 0.0
		if rel {
			ox, oy = cx, cy
		}
		switch cmd | 0x20 {
		case 'm':
			v, err := nums(2)
			if err != nil {
				return nil, err
			}
			cx, cy = ox+v[0], oy+v[1]
			sx, sy = cx, cy
			p.MoveTo(cx, cy)
			cmd = 'L' | (cmd & 0x20) // further pairs are lines
		case 'l':
			v, err := nums(2)
			if err != nil {
				return nil, err
			}
			cx, cy = ox+v[0], oy+v[1]
			p.LineTo(cx, cy)
		case 'h':
			v, err := num()
			if err != nil {
				return nil, err
			}
			cx = ox + v
			p.LineTo(cx, cy)
		case 'v':
			v, err := num()
			if err != nil {
				return nil, err
			}
			cy = oy + v
			p.LineTo(cx, cy)
		case 'c':
			v, err := nums(6)
			if err != nil {
				return nil, err
			}
			p.CubicTo(ox+v[0], oy+v[1], ox+v[2], oy+v[3], ox+v[4], oy+v[5])
			cx, cy = ox+v[4], oy+v[5]
		case 'q':
			v, err := nums(4)
			if err != nil {
				return nil, err
			}
			p.QuadTo(ox+v[0], oy+v[1], ox+v[2], oy+v[3])
			cx, cy = ox+v[2], oy+v[3]
		case 'z':
			p.Close()
			cx, cy = sx, sy
			cmd = 0
		}
	}
	return p, nil
}

type errSVG string

func (e errSVG) Error() string { return "svg path: " + string(e) }

// svgTokens splits path data into commands and numbers ("1.5-2" is two
// numbers, as is "1.5.5").
func svgTokens(d string) []string {
	var out []string
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	dot := false
	for i := 0; i < len(d); i++ {
		c := d[i]
		switch {
		case strings.IndexByte("MmLlHhVvCcQqZz", c) >= 0:
			flush()
			out = append(out, string(c))
			dot = false
		case c == ' ' || c == ',' || c == '\t' || c == '\n' || c == '\r':
			flush()
			dot = false
		case c == '-' || c == '+':
			if cur.Len() > 0 {
				if prev := d[i-1]; prev != 'e' && prev != 'E' {
					flush()
					dot = false
				}
			}
			cur.WriteByte(c)
		case c == '.':
			if dot {
				flush()
			}
			dot = true
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}
