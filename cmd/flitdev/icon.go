package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"

	"github.com/motivated2die/flit/internal/ui"
)

// makeIcon writes assets/flit.ico from assets/logo.png, scaled by ui.AppIcon.
func makeIcon(root string) error {
	var imgs []*image.RGBA
	// Every size Windows asks for from 100% to 400% scaling (16-px icons
	// at 16-64, 24 at 24-96, 32 at 32-128, 48 at 48-192 and the 256 tile),
	// so it never has to scale one and blur it.
	for _, s := range []int{16, 20, 24, 28, 30, 32, 36, 40, 48, 56, 60, 64, 72, 80, 96, 128, 192, 256} {
		imgs = append(imgs, ui.AppIcon(s))
	}
	b, err := ui.ICO(imgs)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "assets", "flit.ico")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n", path, len(b))
	return nil
}
