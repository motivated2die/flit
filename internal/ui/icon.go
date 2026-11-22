package ui

import (
	"embed"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Icons are Tabler's SVG files in icons/ (MIT, see icons/README.md),
// embedded at build time. They are drawn as Direct2D polygons in any
// colour, so an icon follows its button's hover and state colours and is
// crisp at every scale: each one is parsed once, its curves and arcs
// flattened finely enough for the size it is drawn at, and its strokes
// outlined (round caps and joins, as Tabler's are) into polygons that a
// single fill draws. flit's own code; the idea of embedding Tabler's files
// follows Resound (references/resound.md).

//go:embed icons/*.svg
var iconFS embed.FS

// iconStroke is the stroke width in the view box (Tabler's 24-unit box).
const iconStroke = 2

// iconShape is an icon in view-box units: polylines to stroke (and
// whether each is closed) and polygons to fill.
type iconShape struct {
	box     float64 // view box size (square)
	strokes []iconLine
	fills   [][]vec
}

type iconLine struct {
	pts    []vec
	closed bool
}

type vec struct{ x, y float64 }

var (
	iconShapes = map[string]*iconShape{}  // parsed (UI thread only)
	iconPolys  = map[iconKey][][]PointF{} // outlined at a size, at (0, 0)
)

type iconKey struct {
	name string
	size float32
}

// iconOutline returns icon name as polygons for a size×size box at (0, 0),
// nil if there is no such icon.
func iconOutline(name string, size float32) [][]PointF {
	k := iconKey{name, size}
	if p, ok := iconPolys[k]; ok {
		return p
	}
	sh, ok := iconShapes[name]
	if !ok {
		var err error
		if sh, err = loadIcon(name); err != nil {
			sh = nil
		}
		iconShapes[name] = sh
	}
	if sh == nil {
		return nil
	}
	if len(iconPolys) > 512 { // sizes come and go with DPI
		clear(iconPolys)
	}
	p := sh.outline(float64(size))
	iconPolys[k] = p
	return p
}

// DrawIcon draws icon name centred in r at size (device pixels) in colour k.
func DrawIcon(cv Canvas, name string, r RectF, size float32, k ColorF) {
	polys := iconOutline(name, size)
	if len(polys) == 0 {
		return
	}
	x := float32(math.Round(float64((r.L + r.R - size) / 2)))
	y := float32(math.Round(float64((r.T + r.B - size) / 2)))
	out := make([][]PointF, len(polys))
	for i, p := range polys {
		q := make([]PointF, len(p))
		for j, v := range p {
			q[j] = PointF{v.X + x, v.Y + y}
		}
		out[i] = q
	}
	cv.Polygons(out, k)
}

// IconNames lists the embedded icons (tests).
func IconNames() []string {
	ents, _ := iconFS.ReadDir("icons")
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".svg"); ok {
			out = append(out, n)
		}
	}
	return out
}

// svgElem is what an icon's elements may carry.
type svgElem struct {
	XMLName xml.Name
	D       string `xml:"d,attr"`
	Fill    string `xml:"fill,attr"`
	Stroke  string `xml:"stroke,attr"`
	CX      string `xml:"cx,attr"`
	CY      string `xml:"cy,attr"`
	R       string `xml:"r,attr"`
	X1      string `xml:"x1,attr"`
	Y1      string `xml:"y1,attr"`
	X2      string `xml:"x2,attr"`
	Y2      string `xml:"y2,attr"`
	Points  string `xml:"points,attr"`
}

type svgDoc struct {
	ViewBox string    `xml:"viewBox,attr"`
	Fill    string    `xml:"fill,attr"`
	Stroke  string    `xml:"stroke,attr"`
	Elems   []svgElem `xml:",any"`
}

func loadIcon(name string) (*iconShape, error) {
	b, err := iconFS.ReadFile("icons/" + name + ".svg")
	if err != nil {
		return nil, err
	}
	var doc svgDoc
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("icon %s: %w", name, err)
	}
	sh := &iconShape{box: 24}
	if f := strings.Fields(doc.ViewBox); len(f) == 4 {
		if v, err := strconv.ParseFloat(f[2], 64); err == nil && v > 0 {
			sh.box = v
		}
	}
	inherit := func(v, root string) string {
		if v == "" {
			return root
		}
		return v
	}
	num := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	for _, e := range doc.Elems {
		var lines []iconLine
		switch e.XMLName.Local {
		case "path":
			lines, err = parsePath(e.D)
			if err != nil {
				return nil, fmt.Errorf("icon %s: %w", name, err)
			}
		case "circle":
			lines = []iconLine{ellipse(num(e.CX), num(e.CY), num(e.R))}
		case "line":
			lines = []iconLine{{pts: []vec{{num(e.X1), num(e.Y1)}, {num(e.X2), num(e.Y2)}}}}
		case "polyline", "polygon":
			f := strings.FieldsFunc(e.Points, func(r rune) bool { return r == ',' || r == ' ' })
			var l iconLine
			for i := 0; i+1 < len(f); i += 2 {
				l.pts = append(l.pts, vec{num(f[i]), num(f[i+1])})
			}
			l.closed = e.XMLName.Local == "polygon"
			lines = []iconLine{l}
		default:
			continue
		}
		fill, stroke := inherit(e.Fill, doc.Fill), inherit(e.Stroke, doc.Stroke)
		if fill != "" && fill != "none" {
			for _, l := range lines {
				if len(l.pts) >= 3 {
					sh.fills = append(sh.fills, l.pts)
				}
			}
		}
		if stroke != "" && stroke != "none" {
			sh.strokes = append(sh.strokes, lines...)
		}
	}
	return sh, nil
}

// ellipse is a circle as a closed polyline.
func ellipse(cx, cy, r float64) iconLine {
	var l iconLine
	for i := range 48 {
		a := 2 * math.Pi * float64(i) / 48
		l.pts = append(l.pts, vec{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	l.closed = true
	return l
}

// parsePath reads SVG path data (all commands, absolute and relative) into
// polylines; curves and arcs are flattened finely (in view-box units:
// fine for icons up to a few hundred pixels).
func parsePath(d string) ([]iconLine, error) {
	tok := pathTokens(d)
	var out []iconLine
	var cur iconLine
	var p, start, ctrl vec // current point, subpath start, last control point
	var prev byte
	flush := func() {
		if len(cur.pts) > 0 {
			out = append(out, cur)
		}
		cur = iconLine{}
	}
	lineTo := func(q vec) {
		if len(cur.pts) == 0 {
			cur.pts = append(cur.pts, p)
		}
		cur.pts = append(cur.pts, q)
		p = q
	}
	var cmd byte
	i := 0
	nums := func(n int) ([]float64, bool) {
		if i+n > len(tok) {
			return nil, false
		}
		v := make([]float64, n)
		for k := range n {
			if tok[i+k].cmd != 0 {
				return nil, false
			}
			v[k] = tok[i+k].v
		}
		i += n
		return v, true
	}
	for i < len(tok) {
		if tok[i].cmd != 0 {
			cmd = tok[i].cmd
			i++
		} else if cmd == 0 {
			return nil, fmt.Errorf("path data starts with a number")
		}
		rel := cmd >= 'a'
		up := cmd &^ 0x20
		off := func(x, y float64) vec {
			if rel {
				return vec{p.x + x, p.y + y}
			}
			return vec{x, y}
		}
		switch up {
		case 'Z':
			if len(cur.pts) > 0 {
				cur.closed = true
			}
			p = start
			flush()
			prev = 'Z'
			continue
		case 'M':
			v, ok := nums(2)
			if !ok {
				return nil, fmt.Errorf("bad M")
			}
			flush()
			p = off(v[0], v[1])
			start = p
			// Further pairs are line-tos.
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
		case 'L':
			v, ok := nums(2)
			if !ok {
				return nil, fmt.Errorf("bad L")
			}
			lineTo(off(v[0], v[1]))
		case 'H':
			v, ok := nums(1)
			if !ok {
				return nil, fmt.Errorf("bad H")
			}
			x := v[0]
			if rel {
				x += p.x
			}
			lineTo(vec{x, p.y})
		case 'V':
			v, ok := nums(1)
			if !ok {
				return nil, fmt.Errorf("bad V")
			}
			y := v[0]
			if rel {
				y += p.y
			}
			lineTo(vec{p.x, y})
		case 'C', 'S':
			n := 6
			if up == 'S' {
				n = 4
			}
			v, ok := nums(n)
			if !ok {
				return nil, fmt.Errorf("bad %c", cmd)
			}
			var c1 vec
			if up == 'S' {
				c1 = p
				if prev == 'C' || prev == 'S' {
					c1 = vec{2*p.x - ctrl.x, 2*p.y - ctrl.y}
				}
				v = append([]float64{0, 0}, v...)
			} else {
				c1 = off(v[0], v[1])
			}
			c2, e := off(v[2], v[3]), off(v[4], v[5])
			p0 := p
			for k := 1; k <= 16; k++ {
				t := float64(k) / 16
				u := 1 - t
				lineTo(vec{
					u*u*u*p0.x + 3*u*u*t*c1.x + 3*u*t*t*c2.x + t*t*t*e.x,
					u*u*u*p0.y + 3*u*u*t*c1.y + 3*u*t*t*c2.y + t*t*t*e.y,
				})
			}
			ctrl = c2
		case 'Q', 'T':
			n := 4
			if up == 'T' {
				n = 2
			}
			v, ok := nums(n)
			if !ok {
				return nil, fmt.Errorf("bad %c", cmd)
			}
			var c1 vec
			if up == 'T' {
				c1 = p
				if prev == 'Q' || prev == 'T' {
					c1 = vec{2*p.x - ctrl.x, 2*p.y - ctrl.y}
				}
				v = append([]float64{0, 0}, v...)
			} else {
				c1 = off(v[0], v[1])
			}
			e := off(v[2], v[3])
			p0 := p
			for k := 1; k <= 12; k++ {
				t := float64(k) / 12
				u := 1 - t
				lineTo(vec{u*u*p0.x + 2*u*t*c1.x + t*t*e.x, u*u*p0.y + 2*u*t*c1.y + t*t*e.y})
			}
			ctrl = c1
		case 'A':
			v, ok := nums(7)
			if !ok {
				return nil, fmt.Errorf("bad A")
			}
			e := off(v[5], v[6])
			for _, q := range arcPoints(p, e, v[0], v[1], v[2], v[3] != 0, v[4] != 0) {
				lineTo(q)
			}
		default:
			return nil, fmt.Errorf("unknown path command %c", cmd)
		}
		prev = up
	}
	flush()
	return out, nil
}

type pathTok struct {
	cmd byte
	v   float64
}

// pathTokens splits path data into commands and numbers ("1.5.5" is two
// numbers, "-1-2" too, as SVG allows).
func pathTokens(d string) []pathTok {
	var out []pathTok
	i := 0
	for i < len(d) {
		c := d[i]
		switch {
		case c == ' ' || c == ',' || c == '\n' || c == '\t' || c == '\r':
			i++
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			if c != 'e' && c != 'E' {
				out = append(out, pathTok{cmd: c})
			}
			i++
		default:
			j := i
			if d[j] == '-' || d[j] == '+' {
				j++
			}
			dot := false
			for j < len(d) {
				ch := d[j]
				if ch >= '0' && ch <= '9' {
					j++
				} else if ch == '.' && !dot {
					dot = true
					j++
				} else if (ch == 'e' || ch == 'E') && j+1 < len(d) {
					j++
					if d[j] == '-' || d[j] == '+' {
						j++
					}
				} else {
					break
				}
			}
			if j == i {
				i++ // not a number: skip it
				continue
			}
			v, _ := strconv.ParseFloat(d[i:j], 64)
			out = append(out, pathTok{v: v})
			i = j
		}
	}
	return out
}

// arcPoints flattens an SVG elliptical arc from p to e (the endpoint to
// centre conversion of the SVG specification, appendix B.2.4), without p.
func arcPoints(p, e vec, rx, ry, rotDeg float64, large, sweep bool) []vec {
	if rx == 0 || ry == 0 || (p == e) {
		return []vec{e}
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	phi := rotDeg * math.Pi / 180
	cos, sin := math.Cos(phi), math.Sin(phi)
	dx, dy := (p.x-e.x)/2, (p.y-e.y)/2
	x1 := cos*dx + sin*dy
	y1 := -sin*dx + cos*dy
	// Radii too small for the distance grow to fit.
	if l := x1*x1/(rx*rx) + y1*y1/(ry*ry); l > 1 {
		s := math.Sqrt(l)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1*y1 - ry*ry*x1*x1
	den := rx*rx*y1*y1 + ry*ry*x1*x1
	k := 0.0
	if den > 0 && num > 0 {
		k = math.Sqrt(num / den)
	}
	if large == sweep {
		k = -k
	}
	cx1, cy1 := k*rx*y1/ry, -k*ry*x1/rx
	cx := cos*cx1 - sin*cy1 + (p.x+e.x)/2
	cy := sin*cx1 + cos*cy1 + (p.y+e.y)/2
	angle := func(ux, uy, vx, vy float64) float64 {
		return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	}
	t1 := angle(1, 0, (x1-cx1)/rx, (y1-cy1)/ry)
	dt := angle((x1-cx1)/rx, (y1-cy1)/ry, (-x1-cx1)/rx, (-y1-cy1)/ry)
	if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	} else if sweep && dt < 0 {
		dt += 2 * math.Pi
	}
	n := max(4, int(math.Ceil(math.Abs(dt)/(math.Pi/24))))
	out := make([]vec, 0, n)
	for i := 1; i <= n; i++ {
		t := t1 + dt*float64(i)/float64(n)
		x, y := rx*math.Cos(t), ry*math.Sin(t)
		out = append(out, vec{cos*x - sin*y + cx, sin*x + cos*y + cy})
	}
	out[len(out)-1] = e // exactly
	return out
}

// outline turns the icon into polygons for a size×size box: each stroke
// segment a rectangle, a disc at every end and sharp bend (round caps and
// joins), all turning the same way so that one non-zero fill draws their
// union without seams; then the filled shapes as they are.
func (sh *iconShape) outline(size float64) [][]PointF {
	s := size / sh.box
	hw := iconStroke * s / 2
	if hw < 0.5 { // never thinner than a pixel
		hw = 0.5
	}
	var out [][]PointF
	add := func(poly []vec) {
		// Same orientation for all (positive area in screen coordinates).
		area := 0.0
		for i := range poly {
			a, b := poly[i], poly[(i+1)%len(poly)]
			area += a.x*b.y - b.x*a.y
		}
		q := make([]PointF, len(poly))
		for i, v := range poly {
			j := i
			if area < 0 {
				j = len(poly) - 1 - i
			}
			q[j] = PointF{float32(v.x), float32(v.y)}
		}
		out = append(out, q)
	}
	discN := max(8, min(24, int(math.Ceil(hw*2*math.Pi/0.75))))
	disc := func(c vec) {
		poly := make([]vec, discN)
		for i := range poly {
			a := 2 * math.Pi * float64(i) / float64(discN)
			poly[i] = vec{c.x + hw*math.Cos(a), c.y + hw*math.Sin(a)}
		}
		add(poly)
	}
	for _, l := range sh.strokes {
		pts := make([]vec, 0, len(l.pts)+1)
		for _, v := range l.pts {
			q := vec{v.x * s, v.y * s}
			if n := len(pts); n > 0 && math.Hypot(q.x-pts[n-1].x, q.y-pts[n-1].y) < 1e-6 {
				continue
			}
			pts = append(pts, q)
		}
		if l.closed && len(pts) > 2 {
			pts = append(pts, pts[0])
		}
		if len(pts) == 1 { // a dot
			disc(pts[0])
			continue
		}
		for i := 0; i+1 < len(pts); i++ {
			a, b := pts[i], pts[i+1]
			dx, dy := b.x-a.x, b.y-a.y
			l := math.Hypot(dx, dy)
			nx, ny := -dy/l*hw, dx/l*hw
			add([]vec{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}})
		}
		// Discs at the ends and where the line bends enough to open a gap.
		for i := range pts {
			end := i == 0 || i == len(pts)-1
			if end && l.closed {
				end = false
				if i == 0 && len(pts) > 2 {
					if bent(pts[len(pts)-2], pts[0], pts[1]) {
						disc(pts[0])
					}
				}
				continue
			}
			if end || bent(pts[i-1], pts[i], pts[i+1]) {
				disc(pts[i])
			}
		}
	}
	for _, f := range sh.fills {
		poly := make([]PointF, len(f))
		for i, v := range f {
			poly[i] = PointF{float32(v.x * s), float32(v.y * s)}
		}
		out = append(out, poly)
	}
	return out
}

// bent reports whether the line a→b→c turns by more than about 12°.
func bent(a, b, c vec) bool {
	ux, uy := b.x-a.x, b.y-a.y
	vx, vy := c.x-b.x, c.y-b.y
	lu, lv := math.Hypot(ux, uy), math.Hypot(vx, vy)
	if lu == 0 || lv == 0 {
		return true
	}
	return (ux*vx+uy*vy)/(lu*lv) < 0.978
}
