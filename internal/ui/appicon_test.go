package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
)

func TestAppIcon(t *testing.T) {
	for _, size := range []int{16, 32, 64, 192, 256} {
		img := AppIcon(size)
		if img.Bounds().Dx() != size {
			t.Fatalf("%d: size %v", size, img.Bounds())
		}
		if c := img.RGBAAt(0, 0); c.A > 100 { // at 16 px a corner pixel is partly tile
			t.Errorf("%d: corner %v, want mostly transparent (rounded)", size, c)
		}
		// The dark tile on the left, half way down.
		if c := img.RGBAAt(size/6, size/2); c.A < 250 || c.R > 0x60 {
			t.Errorf("%d: tile %v, want the dark tile", size, c)
		}
		light := 0
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if c := img.RGBAAt(x, y); c.R > 0xB0 && c.A > 250 {
					light++
				}
			}
		}
		// The feather covers roughly a tenth to a third of the tile.
		if share := float64(light) / float64(size*size); share < 0.06 || share > 0.35 {
			t.Errorf("%d: %.0f%% light pixels: the feather is missing or blotted", size, 100*share)
		}
	}
	// Scaling to the logo's own size gives the logo back.
	src := logoImage()
	n := src.Bounds().Dx()
	img := AppIcon(n)
	for _, p := range [][2]int{{n / 2, n / 2}, {n / 5, n * 4 / 5}, {n * 4 / 5, n / 5}} {
		a, b := src.NRGBAAt(p[0], p[1]), img.RGBAAt(p[0], p[1])
		if d := int(a.R) - int(b.R); d > 2 || d < -2 || a.A != b.A {
			t.Errorf("at %v: logo %v, scaled %v", p, a, b)
		}
	}
}

func TestICO(t *testing.T) {
	b, err := ICO([]*image.RGBA{AppIcon(16), AppIcon(256)})
	if err != nil {
		t.Fatal(err)
	}
	var hdr [3]uint16
	binary.Read(bytes.NewReader(b), binary.LittleEndian, &hdr)
	if hdr != [3]uint16{0, 1, 2} {
		t.Fatalf("header %v", hdr)
	}
	if b[6] != 16 || b[6+16] != 0 { // 256 is written as 0
		t.Fatalf("sizes %d, %d", b[6], b[22])
	}
	off := binary.LittleEndian.Uint32(b[6+16+12:])
	img, err := png.Decode(bytes.NewReader(b[off:]))
	if err != nil || img.Bounds().Dx() != 256 {
		t.Fatalf("second entry: %v %v", err, img.Bounds())
	}
}
