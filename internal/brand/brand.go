// Package brand draws the application's mark.
//
// The mark is a bold white chevron on a blue tile: the V of VPN, and a funnel
// narrowing to a point. It is defined here in normalised coordinates and
// rendered on demand, so the tray icon, the badged tray icon and the Windows
// .ico all come from one description rather than a folder of hand-exported
// bitmaps that drift apart.
//
// The same geometry is drawn as SVG in the frontend; see BrandMark in
// frontend/src/icons.tsx. Change one and change the other.
package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

// Brand colours. Blue is the identity, white the counterpart.
var (
	Blue  = color.NRGBA{R: 0x25, G: 0x63, B: 0xeb, A: 0xff}
	White = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	// Green marks a live connection, matching the "protected" colour in the UI.
	Green = color.NRGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}
)

// Geometry, as fractions of the tile so every size renders identically.
const (
	cornerRadius = 0.22

	chevronLeftX  = 0.26
	chevronRightX = 0.74
	chevronTopY   = 0.34
	chevronTipY   = 0.68
	chevronHalf   = 0.085

	// The badge sits in the bottom-right corner with a ring of tile colour
	// around it, so it reads as a separate token rather than a smudge on the
	// chevron.
	//
	// Pushed far enough into the corner to clear the chevron's right arm: at
	// 16px the ring is sub-pixel, so any overlap eats the mark itself rather
	// than being separated by it.
	badgeCentre = 0.78
	badgeRadius = 0.205
	badgeRing   = 0.05
)

// supersample is the factor each axis is oversampled by before averaging down.
// Four is enough to make the curves clean at 16px and costs nothing at these
// sizes; the alternative would be pulling in a rasteriser dependency for one
// chevron.
const supersample = 4

// Icon renders the mark at the given pixel size.
//
// badge, when non-nil, adds a status dot in the corner.
func Icon(size int, badge color.Color) image.Image {
	if size < 1 {
		size = 1
	}
	big := size * supersample
	hi := image.NewNRGBA(image.Rect(0, 0, big, big))

	var badgeNRGBA color.NRGBA
	hasBadge := badge != nil
	if hasBadge {
		r, g, b, a := badge.RGBA()
		badgeNRGBA = color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
	}

	for y := 0; y < big; y++ {
		for x := 0; x < big; x++ {
			// Sample at pixel centres in normalised space.
			u := (float64(x) + 0.5) / float64(big)
			v := (float64(y) + 0.5) / float64(big)

			var c color.NRGBA
			switch {
			case hasBadge && within(u, v, badgeCentre, badgeCentre, badgeRadius):
				// The ring is drawn in the tile colour so the badge stays
				// distinct even against a white chevron.
				if within(u, v, badgeCentre, badgeCentre, badgeRadius-badgeRing) {
					c = badgeNRGBA
				} else {
					c = Blue
				}
			case onChevron(u, v):
				c = White
			case insideTile(u, v):
				c = Blue
			default:
				c = color.NRGBA{}
			}
			hi.SetNRGBA(x, y, c)
		}
	}

	return downsample(hi, size)
}

// insideTile reports whether a point is within the rounded square.
func insideTile(u, v float64) bool {
	r := cornerRadius
	// Distance from the rounded-rect's inner box, which is the standard way to
	// get all four corners right with one expression.
	dx := math.Max(math.Max(r-u, u-(1-r)), 0)
	dy := math.Max(math.Max(r-v, v-(1-r)), 0)
	return math.Hypot(dx, dy) <= r
}

// onChevron reports whether a point falls on the stroked V.
//
// Measuring distance to the two segments gives round joins and caps for free,
// matching the SVG's stroke-linejoin and stroke-linecap.
func onChevron(u, v float64) bool {
	const tipX = 0.5
	left := distanceToSegment(u, v, chevronLeftX, chevronTopY, tipX, chevronTipY)
	right := distanceToSegment(u, v, tipX, chevronTipY, chevronRightX, chevronTopY)
	return math.Min(left, right) <= chevronHalf
}

func within(u, v, cx, cy, r float64) bool {
	return math.Hypot(u-cx, v-cy) <= r
}

func distanceToSegment(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	lengthSquared := dx*dx + dy*dy
	if lengthSquared == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / lengthSquared
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
}

// downsample averages each supersample x supersample block, premultiplying so
// partially covered edge pixels do not pick up a halo from transparent
// neighbours.
func downsample(src *image.NRGBA, size int) image.Image {
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	area := float64(supersample * supersample)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var rSum, gSum, bSum, aSum float64
			for dy := 0; dy < supersample; dy++ {
				for dx := 0; dx < supersample; dx++ {
					c := src.NRGBAAt(x*supersample+dx, y*supersample+dy)
					a := float64(c.A) / 255
					rSum += float64(c.R) * a
					gSum += float64(c.G) * a
					bSum += float64(c.B) * a
					aSum += a
				}
			}
			if aSum == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(rSum / aSum)),
				G: uint8(math.Round(gSum / aSum)),
				B: uint8(math.Round(bSum / aSum)),
				A: uint8(math.Round(aSum / area * 255)),
			})
		}
	}
	return out
}

// PNG encodes the mark at one size.
func PNG(size int, badge color.Color) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, Icon(size, badge)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Sheet renders the mark onto an opaque background, for contexts that cannot
// cope with transparency.
func Sheet(size int, background color.Color) image.Image {
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.Draw(out, out.Bounds(), &image.Uniform{background}, image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), Icon(size, nil), image.Point{}, draw.Over)
	return out
}
