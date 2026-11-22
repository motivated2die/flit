package ui

import (
	"image"
	"math"
	"testing"
)

func alphaAt(img *image.RGBA, x, y int) uint8 { return img.RGBAAt(x, y).A }

func TestFillSquareExact(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	Fill(img, (&Path{}).MoveTo(2, 2).LineTo(6, 2).LineTo(6, 6).LineTo(2, 6).Close(), ColText)
	if alphaAt(img, 3, 3) != 255 || alphaAt(img, 1, 3) != 0 || alphaAt(img, 6, 3) != 0 {
		t.Fatalf("inside %d, left %d, right %d", alphaAt(img, 3, 3), alphaAt(img, 1, 3), alphaAt(img, 6, 3))
	}
	// A half-pixel edge is half covered.
	img = image.NewRGBA(image.Rect(0, 0, 10, 10))
	Fill(img, (&Path{}).MoveTo(2, 2).LineTo(5.5, 2).LineTo(5.5, 6).LineTo(2, 6).Close(), ColText)
	if a := alphaAt(img, 5, 3); a < 120 || a > 135 {
		t.Fatalf("half-covered pixel alpha %d", a)
	}
}

func TestCircleArea(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	Fill(img, (&Path{}).Circle(16, 16, 10), ColText)
	var sum float64
	for i := 3; i < len(img.Pix); i += 4 {
		sum += float64(img.Pix[i]) / 255
	}
	if want := math.Pi * 100; math.Abs(sum-want)/want > 0.02 {
		t.Fatalf("area %.1f, want %.1f", sum, want)
	}
}

func TestEraseAndClip(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	Fill(img, (&Path{}).Circle(10, 10, 8), ColText)
	Erase(img, (&Path{}).Circle(10, 10, 3))
	if alphaAt(img, 10, 10) != 0 || alphaAt(img, 10, 4) != 255 {
		t.Fatalf("centre %d, ring %d", alphaAt(img, 10, 10), alphaAt(img, 10, 4))
	}
	img = image.NewRGBA(image.Rect(0, 0, 20, 20))
	FillClipped(img, (&Path{}).Circle(10, 10, 8), ColText, image.Rect(0, 10, 20, 20))
	if alphaAt(img, 10, 5) != 0 || alphaAt(img, 10, 15) != 255 {
		t.Fatal("clip ignored")
	}
}

func TestCapsule(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	Fill(img, (&Path{}).Capsule(4, 10, 16, 10, 4), ColText)
	if alphaAt(img, 10, 10) != 255 || alphaAt(img, 10, 13) != 0 || alphaAt(img, 2, 10) < 100 {
		t.Fatalf("mid %d, below %d, cap %d", alphaAt(img, 10, 10), alphaAt(img, 10, 13), alphaAt(img, 2, 10))
	}
}
