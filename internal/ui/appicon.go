package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math"
	"sync"

	"github.com/motivated2die/flit/assets"
)

// AppIcon is the application icon (exe, Start Menu, window, Installed
// apps) at size×size: assets/logo.png, the feather on a dark rounded tile,
// scaled with a Catmull-Rom filter (widened when shrinking, so small sizes
// average rather than skip pixels). Straight alpha, as newIcon and ICO
// take it.
func AppIcon(size int) *image.RGBA {
	src := logoImage()
	if src == nil {
		return image.NewRGBA(image.Rect(0, 0, size, size))
	}
	return scaleStraight(src, size)
}

var logo struct {
	once sync.Once
	img  *image.NRGBA
}

// logoImage decodes the embedded logo once (nil if it can't be read).
func logoImage() *image.NRGBA {
	logo.once.Do(func() {
		m, err := png.Decode(bytes.NewReader(assets.LogoPNG))
		if err != nil {
			return
		}
		b := m.Bounds()
		n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				n.Set(x, y, m.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		logo.img = n
	})
	return logo.img
}

// scaleStraight resizes a straight-alpha image to size×size, filtering
// in premultiplied colour (so transparent pixels don't bleed their colour
// into the edge), and returns straight alpha again.
func scaleStraight(src *image.NRGBA, size int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	// Premultiplied floats, 4 per pixel.
	pre := make([]float64, 4*sw*sh)
	for i := 0; i < sw*sh; i++ {
		p := src.Pix[4*i : 4*i+4]
		a := float64(p[3]) / 255
		pre[4*i], pre[4*i+1], pre[4*i+2], pre[4*i+3] = float64(p[0])*a, float64(p[1])*a, float64(p[2])*a, float64(p[3])
	}
	tmp := resample1D(pre, sw, sh, size, true) // size × sh
	out := resample1D(tmp, size, sh, size, false)
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for i := 0; i < size*size; i++ {
		a := math.Max(0, math.Min(255, out[4*i+3]))
		p := img.Pix[4*i : 4*i+4]
		if a < 0.5 {
			continue
		}
		for c := range 3 { // colour·alpha (0..255 · 0..1) back to colour
			p[c] = uint8(math.Max(0, math.Min(255, out[4*i+c]*255/a)) + 0.5)
		}
		p[3] = uint8(a + 0.5)
	}
	return img
}

// resample1D scales a w×h buffer of 4 floats per pixel to n along x
// (horizontal) or y, with Catmull-Rom.
func resample1D(src []float64, w, h, n int, horizontal bool) []float64 {
	from := h
	if horizontal {
		from = w
	}
	scale := float64(from) / float64(n)
	support := 2 * math.Max(1, scale)
	ow, oh := n, h
	if !horizontal {
		ow, oh = w, n
	}
	out := make([]float64, 4*ow*oh)
	for o := range n {
		centre := (float64(o)+0.5)*scale - 0.5
		lo, hi := int(math.Floor(centre-support)), int(math.Ceil(centre+support))
		var taps []int
		var weights []float64
		sum := 0.0
		for i := lo; i <= hi; i++ {
			wt := catmullRom((float64(i) - centre) / math.Max(1, scale))
			if wt == 0 {
				continue
			}
			taps = append(taps, max(0, min(from-1, i)))
			weights = append(weights, wt)
			sum += wt
		}
		for j := range weights {
			weights[j] /= sum
		}
		lines := h
		if !horizontal {
			lines = w
		}
		for l := range lines {
			var acc [4]float64
			for j, t := range taps {
				si := 4 * (l*w + t)
				if !horizontal {
					si = 4 * (t*w + l)
				}
				for c := range 4 {
					acc[c] += src[si+c] * weights[j]
				}
			}
			di := 4 * (l*ow + o)
			if !horizontal {
				di = 4 * (o*ow + l)
			}
			copy(out[di:di+4], acc[:])
		}
	}
	return out
}

// catmullRom is the Catmull-Rom cubic (B=0, C=0.5).
func catmullRom(x float64) float64 {
	x = math.Abs(x)
	switch {
	case x < 1:
		return 1.5*x*x*x - 2.5*x*x + 1
	case x < 2:
		return -0.5*x*x*x + 2.5*x*x - 4*x + 2
	}
	return 0
}

// ICO encodes images as a Windows icon file with PNG-compressed entries
// (supported since Windows Vista for every size).
func ICO(imgs []*image.RGBA) ([]byte, error) {
	var pngs [][]byte
	for _, img := range imgs {
		// The images hold straight alpha: encode them as such (png reads
		// an image.RGBA as premultiplied).
		n := &image.NRGBA{Pix: img.Pix, Stride: img.Stride, Rect: img.Rect}
		var b bytes.Buffer
		if err := png.Encode(&b, n); err != nil {
			return nil, err
		}
		pngs = append(pngs, b.Bytes())
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(imgs))})
	offset := 6 + 16*len(imgs)
	for i, img := range imgs {
		w := img.Bounds().Dx()
		dim := byte(w)
		if w >= 256 {
			dim = 0
		}
		binary.Write(&out, binary.LittleEndian, struct {
			W, H, Colors, Reserved byte
			Planes, Bits           uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(pngs[i])), uint32(offset)})
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes(), nil
}
