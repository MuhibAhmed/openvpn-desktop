// Command genicons writes the icon files the build needs.
//
// The mark itself lives in internal/brand; this only puts it on disk in the
// formats the Windows toolchain and the packaging steps expect. Run it after
// changing the mark:
//
//	go run ./cmd/genicons
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"github.com/MuhibAhmed/openvpn-desktop/internal/brand"
)

func main() {
	targets := []struct {
		path  string
		write func(*os.File) error
	}{
		{
			// Compiled into the executable by "wails3 generate syso".
			path: filepath.Join("build", "windows", "icon.ico"),
			write: func(f *os.File) error {
				data, err := brand.ICO()
				if err != nil {
					return err
				}
				_, err = f.Write(data)
				return err
			},
		},
		{
			// Used by the packaging tasks and as a generic app image.
			path: filepath.Join("build", "appicon.png"),
			write: func(f *os.File) error {
				return png.Encode(f, brand.Icon(1024, nil))
			},
		},
		{
			// A figure for the README. Rendered from the same bytes the tray
			// receives rather than captured from a desktop, so it shows the
			// real 16px pixels and does not drag anyone's wallpaper into the
			// repository.
			path:  filepath.Join("docs", "screenshots", "tray-icon.png"),
			write: func(f *os.File) error { return png.Encode(f, trayFigure()) },
		},
	}

	for _, t := range targets {
		if err := write(t.path, t.write); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", t.path, err)
			os.Exit(1)
		}
		info, _ := os.Stat(t.path)
		fmt.Printf("wrote %s (%d bytes)\n", t.path, info.Size())
	}
}

// trayFigure shows the idle and connected tray icons side by side, magnified
// with no smoothing so every pixel Windows draws is visible.
func trayFigure() image.Image {
	const (
		iconSize = 16 // the size the notification area actually asks for
		scale    = 10
		pad      = 36
		gap      = 56
	)
	drawn := iconSize * scale
	width := pad*2 + drawn*2 + gap
	height := pad*2 + drawn

	// The colour of a dark Windows taskbar, so the icons are judged against
	// the background they will really sit on.
	taskbar := color.NRGBA{R: 0x20, G: 0x20, B: 0x20, A: 0xff}

	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(out, out.Bounds(), &image.Uniform{taskbar}, image.Point{}, draw.Src)

	for i, badge := range []color.Color{nil, brand.Green} {
		src := brand.Icon(iconSize, badge)
		originX := pad + i*(drawn+gap)
		for y := 0; y < drawn; y++ {
			for x := 0; x < drawn; x++ {
				c := src.At(x/scale, y/scale)
				out.Set(originX+x, pad+y, blend(c, taskbar))
			}
		}
	}
	return out
}

// blend composites a partially transparent source over an opaque background,
// which the magnified view needs so anti-aliased edges do not turn black.
func blend(src color.Color, over color.NRGBA) color.NRGBA {
	r, g, b, a := src.RGBA()
	if a == 0 {
		return over
	}
	alpha := float64(a) / 0xffff
	mix := func(s uint32, o uint8) uint8 {
		// s is alpha-premultiplied, which is exactly what "over" wants.
		return uint8(float64(s>>8) + float64(o)*(1-alpha))
	}
	return color.NRGBA{R: mix(r, over.R), G: mix(g, over.G), B: mix(b, over.B), A: 0xff}
}

func write(path string, fn func(*os.File) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return fn(f)
}
