package brand

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// TestIconIsLegibleAtFaviconSize is the size that drove the design. A mark whose
// silhouette collapses at 16px is useless in a tray or a browser tab, so this
// checks the two colours are still both clearly present.
func TestIconIsLegibleAtFaviconSize(t *testing.T) {
	img := Icon(16, nil)

	var blueish, whiteish int
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue
			}
			switch {
			case r > 0xd000 && g > 0xd000 && b > 0xd000:
				whiteish++
			case b > r && b > g:
				blueish++
			}
		}
	}

	// The chevron has to be a real presence, not a few stray pixels.
	if whiteish < 20 {
		t.Errorf("only %d white pixels at 16px; the chevron has collapsed", whiteish)
	}
	if blueish < 100 {
		t.Errorf("only %d blue pixels at 16px; the tile has collapsed", blueish)
	}
}

func TestIconCorners(t *testing.T) {
	img := Icon(64, nil)
	// The tile is rounded, so the very corner must be transparent.
	if _, _, _, a := img.At(0, 0).RGBA(); a > 0x2000 {
		t.Errorf("top-left corner alpha = %d, want it rounded away", a)
	}
	// The centre sits on the chevron.
	r, g, b, a := img.At(32, 40).RGBA()
	if a < 0x8000 || r < 0xd000 || g < 0xd000 || b < 0xd000 {
		t.Errorf("centre pixel = (%d,%d,%d,%d), want white chevron", r, g, b, a)
	}
}

// TestBadgeOnlyWhenConnected guards the tray behaviour that was asked for: a
// green marker when the tunnel is up, and nothing when it is not.
func TestBadgeOnlyWhenConnected(t *testing.T) {
	countGreen := func(img image.Image) int {
		n := 0
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bb, a := img.At(x, y).RGBA()
				if a > 0x8000 && g > r && g > bb && g > 0x8000 {
					n++
				}
			}
		}
		return n
	}

	if n := countGreen(Icon(32, nil)); n != 0 {
		t.Errorf("idle icon has %d green pixels, want none", n)
	}
	if n := countGreen(Icon(32, Green)); n < 40 {
		t.Errorf("connected icon has only %d green pixels; the badge is too small to notice", n)
	}
}

// TestBadgeDoesNotEatTheChevron keeps the badge in its corner. At 16px the ring
// separating them is thinner than a pixel, so an overlapping badge takes a bite
// out of the mark instead of sitting beside it.
func TestBadgeDoesNotEatTheChevron(t *testing.T) {
	plain := Icon(64, nil)
	badged := Icon(64, Green)

	countWhite := func(img image.Image) int {
		n := 0
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bb, a := img.At(x, y).RGBA()
				if a > 0x8000 && r > 0xd000 && g > 0xd000 && bb > 0xd000 {
					n++
				}
			}
		}
		return n
	}

	before, after := countWhite(plain), countWhite(badged)
	if before == 0 {
		t.Fatal("no chevron to measure")
	}
	// A little anti-aliasing overlap is tolerable; a visible bite is not.
	if lost := before - after; lost*100/before > 2 {
		t.Errorf("the badge removed %d of %d chevron pixels (%d%%)", lost, before, lost*100/before)
	}
}

func TestTrayIconEncodesPNG(t *testing.T) {
	for _, connected := range []bool{false, true} {
		data, err := TrayIcon(connected)
		if err != nil {
			t.Fatalf("TrayIcon(%v): %v", connected, err)
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Errorf("TrayIcon(%v) is not a valid PNG: %v", connected, err)
		}
	}
}

// TestICOStructure checks the parts Windows actually reads. A malformed
// directory shows up as a blank icon with no error anywhere.
func TestICOStructure(t *testing.T) {
	data, err := ICO()
	if err != nil {
		t.Fatalf("ICO: %v", err)
	}
	if len(data) < 22 {
		t.Fatalf("ICO is only %d bytes", len(data))
	}

	read16 := func(i int) int { return int(data[i]) | int(data[i+1])<<8 }
	read32 := func(i int) int {
		return int(data[i]) | int(data[i+1])<<8 | int(data[i+2])<<16 | int(data[i+3])<<24
	}

	if got := read16(0); got != 0 {
		t.Errorf("reserved = %d, want 0", got)
	}
	if got := read16(2); got != 1 {
		t.Errorf("type = %d, want 1 (icon)", got)
	}
	count := read16(4)
	if count != len(icoSizes) {
		t.Fatalf("image count = %d, want %d", count, len(icoSizes))
	}

	for i := 0; i < count; i++ {
		base := 6 + 16*i
		size := int(data[base])
		want := icoSizes[i]
		if want >= 256 {
			want = 0
		}
		if size != want {
			t.Errorf("entry %d width byte = %d, want %d", i, size, want)
		}

		length := read32(base + 8)
		offset := read32(base + 12)
		if offset+length > len(data) {
			t.Fatalf("entry %d points past the end of the file", i)
		}
		// Each payload must be a decodable PNG of the right dimensions.
		img, err := png.Decode(bytes.NewReader(data[offset : offset+length]))
		if err != nil {
			t.Errorf("entry %d is not a valid PNG: %v", i, err)
			continue
		}
		if got := img.Bounds().Dx(); got != icoSizes[i] {
			t.Errorf("entry %d is %dpx, want %dpx", i, got, icoSizes[i])
		}
	}
}
