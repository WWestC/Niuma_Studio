// Package pixart renders all pixel art procedurally from string maps —
// the binary embeds zero image assets. Since the Ebiten window retired
// (v2 P6) every surface is a plain *image.NRGBA: the server ships the
// layers as PNGs (server/art.go) and the app window's canvas board
// composites them in the browser.
package pixart

import (
	"fmt"
	"image"
	"image/color"
	"strings"
)

// Palette maps single characters in a sprite string to colors.
// '.', ' ' and any unknown rune are transparent.
type Palette map[rune]color.RGBA

// C is a hex color helper: C("#e07a5f").
func C(hex string) color.RGBA {
	h := strings.TrimPrefix(hex, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	var r, g, b uint8
	_, _ = fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

// NewCanvas allocates a transparent native-pixel canvas.
func NewCanvas(w, h int) *image.NRGBA {
	return image.NewNRGBA(image.Rect(0, 0, w, h))
}

// Render converts character rows into a canvas sized to the
// widest/longest row. Rows may be ragged.
func Render(rows []string, pal Palette) *image.NRGBA {
	w := 0
	for _, r := range rows {
		if n := len([]rune(r)); n > w {
			w = n
		}
	}
	// t_76: every grid cell renders as an S×S block — the sprite rides
	// the rescale grid like the furniture does, crisp not stretched
	img := NewCanvas(w*S, len(rows)*S)
	for y, row := range rows {
		for x, ch := range row {
			if c, ok := pal[ch]; ok {
				FillRect(img, x*S, y*S, S, S, c)
			}
		}
	}
	// hair-top highlight (t_76 detail pass): one lighter native
	// streak across the crown — a brush stroke the old 1px grid
	// could never hold. Applied inside Render so mirrored frames
	// stay consistent.
	if h, ok := pal['H']; ok {
		FillRect(img, 5*S, S, 6*S, 1, liftC(h))
	}
	return img
}

// liftC raises each channel toward white — palette-tier color math
// for the accent passes.
func liftC(c color.RGBA) color.RGBA {
	const d = uint8(52)
	return color.RGBA{
		R: min(c.R+d, 255), G: min(c.G+d, 255), B: min(c.B+d, 255), A: c.A,
	}
}

// Mirror returns the rows flipped horizontally.
func Mirror(rows []string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		rs := []rune(row)
		for l, r := 0, len(rs)-1; l < r; l, r = l+1, r-1 {
			rs[l], rs[r] = rs[r], rs[l]
		}
		out[i] = string(rs)
	}
	return out
}

// Size returns the pixel dimensions Render would produce for rows.
func Size(rows []string) (w, h int) {
	for _, r := range rows {
		if n := len([]rune(r)); n > w {
			w = n
		}
	}
	return w, len(rows)
}

// Blit copies src onto dst at (x, y) — the DrawImage(at(x,y))
// successor for plain NRGBA layers. A true over-composite: transparent
// source pixels leave what's beneath untouched — sprites carry
// transparent margins, and copying those raw would punch holes through
// the already-painted layer for the page background to show through.
func Blit(dst, src *image.NRGBA, x, y int) {
	r := src.Bounds()
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			i := dst.PixOffset(xx+x-r.Min.X, yy+y-r.Min.Y)
			if i < 0 || i+3 >= len(dst.Pix) {
				continue
			}
			j := src.PixOffset(xx, yy)
			sa := float64(src.Pix[j+3]) / 255
			if sa == 0 {
				continue
			}
			if sa == 1 {
				dst.Pix[i] = src.Pix[j]
				dst.Pix[i+1] = src.Pix[j+1]
				dst.Pix[i+2] = src.Pix[j+2]
				dst.Pix[i+3] = 255
				continue
			}
			// partial alpha: src-over over the non-premultiplied dst
			da := float64(dst.Pix[i+3]) / 255
			outA := sa + da*(1-sa)
			mix := func(s, d uint8) uint8 {
				return uint8((float64(s)*sa + float64(d)*da*(1-sa)) / outA)
			}
			dst.Pix[i] = mix(src.Pix[j], dst.Pix[i])
			dst.Pix[i+1] = mix(src.Pix[j+1], dst.Pix[i+1])
			dst.Pix[i+2] = mix(src.Pix[j+2], dst.Pix[i+2])
			dst.Pix[i+3] = uint8(min(outA, 1) * 255)
		}
	}
}

// FillRect fills a native-unit rectangle on an NRGBA canvas.
func FillRect(img *image.NRGBA, x, y, w, h int, c color.RGBA) {
	x0, y0 := max(img.Rect.Min.X, x), max(img.Rect.Min.Y, y)
	x1, y1 := min(img.Rect.Max.X, x+w), min(img.Rect.Max.Y, y+h)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	// premultipled NRGBA: scale channels by alpha once per row span
	span := x1 - x0
	row := make([]byte, span*4)
	a := float64(c.A) / 255
	for i := 0; i < span; i++ {
		row[i*4] = uint8(float64(c.R) * a)
		row[i*4+1] = uint8(float64(c.G) * a)
		row[i*4+2] = uint8(float64(c.B) * a)
		row[i*4+3] = c.A
	}
	for yy := y0; yy < y1; yy++ {
		copy(img.Pix[img.PixOffset(x0, yy):img.PixOffset(x0, yy)+len(row)], row)
	}
}
