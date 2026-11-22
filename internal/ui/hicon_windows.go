package ui

import (
	"image"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

// newIcon turns an RGBA image into an HICON with per-pixel alpha: a 32-bit
// top-down DIB in BGRA (straight alpha, as icons take it) and an empty mask.
// The caller destroys it with procDestroyIcon.
func newIcon(img *image.RGBA) uintptr {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	bi := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	color, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == nil {
		return 0
	}
	dst := unsafe.Slice((*byte)(bits), w*h*4)
	for i := 0; i < w*h; i++ {
		p := img.Pix[i*4 : i*4+4]
		dst[i*4+0], dst[i*4+1], dst[i*4+2], dst[i*4+3] = p[2], p[1], p[0], p[3]
	}
	mask, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, 0)
	ii := iconInfo{FIcon: 1, HbmMask: mask, HbmColor: color}
	icon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	procDeleteObject.Call(color)
	procDeleteObject.Call(mask)
	return icon
}

// trayIconSize is the small-icon size for the current DPI.
func trayIconSize() int {
	n, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	if n == 0 {
		return 16
	}
	return int(n)
}

// lightTaskbar reports whether the taskbar uses the light theme.
func lightTaskbar() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("SystemUsesLightTheme")
	return err == nil && v == 1
}
