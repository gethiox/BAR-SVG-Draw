package raster

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// DebugDir, when set, makes Visible write its intermediate images there.
var DebugDir string

func dumpDebug(dir string, w, h int, rgb []float32, label []int32, thick []bool, m *Mask) {
	save := func(name string, f func(i int) color.RGBA) {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < w*h; i++ {
			img.SetRGBA(i%w, i/w, f(i))
		}
		if fh, err := os.Create(filepath.Join(dir, name)); err == nil {
			_ = png.Encode(fh, img)
			fh.Close()
		}
	}
	save("dbg_color.png", func(i int) color.RGBA {
		return color.RGBA{uint8(rgb[3*i] * 255), uint8(rgb[3*i+1] * 255), uint8(rgb[3*i+2] * 255), 255}
	})
	save("dbg_label.png", func(i int) color.RGBA {
		l := uint32(label[i]) * 2654435761
		return color.RGBA{uint8(l >> 24), uint8(l >> 16), uint8(l >> 8), 255}
	})
	save("dbg_lines.png", func(i int) color.RGBA {
		switch {
		case m.B[i] != 0:
			return color.RGBA{0, 0, 0, 255}
		case thick[i]:
			return color.RGBA{200, 220, 255, 255}
		}
		return color.RGBA{255, 255, 255, 255}
	})
}
