// icongen assembles the niuma app icons from the brand asset
// directory (t_112): brand/icon-inverted/ — 小鹿's 定稿 v1.0 (the
// ZCode icon, luminance-inverted per the owner's 拍板) — is the single
// source of truth. Two outputs, same source:
//
//	icongen <iconset-dir>            ten PNG sizes verbatim into a macOS
//	                                  iconset; build.sh folds it into an
//	                                  .icns with iconutil
//	icongen -ico <out.ico>           one multi-size Windows .ico (16–256);
//	                                  build.bat/release.yml compile it into
//	                                  the exe's resources via windres
//
// The old programmatic chalkboard glyph is kept below as the fallback
// when the brand directory is missing (zero-image-assets discipline
// for fresh checkouts predating the assets).
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// palette — the house chalk board colors (ui-style-guide 同源语义).
var (
	frameCol   = color.NRGBA{R: 0x8a, G: 0x6a, B: 0x4a, A: 0xff} // 木框
	boardCol   = color.NRGBA{R: 0x1e, G: 0x24, B: 0x1e, A: 0xff} // 黑板
	chalkCol   = color.NRGBA{R: 0xd8, G: 0xdf, B: 0xd3, A: 0xff} // 粉笔
	speakerCol = color.NRGBA{R: 0xd9, G: 0xb9, B: 0x6a, A: 0xff} // 房主金
)

// inRounded reports whether the pixel is inside a rounded rectangle
// spanning [0,1)² in normalized coordinates with corner radius r —
// the whole rasterizer is this test.
func inRounded(x, y, r float64) bool {
	if x < 0 || x >= 1 || y < 0 || y >= 1 {
		return false
	}
	cx, cy := 0.5, 0.5
	dx, dy := x-cx, y-cy
	ax, ay := abs(dx)-(.5-r), abs(dy)-(.5-r)
	if ax <= 0 || ay <= 0 {
		return true
	}
	return ax*ax+ay*ay <= r*r
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// draw renders the glyph at the given square size.
func draw(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	radius := 0.225
	inset := 0.06 // classic macOS icon margin
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			x := float64(px)/s*(1-2*inset) + inset
			y := float64(py)/s*(1-2*inset) + inset
			switch {
			case inRounded(x, y, radius):
				img.Set(px, py, frameCol)
			case inRounded(x, y, radius-0.035):
				img.Set(px, py, boardCol)
			}
		}
	}
	// chalk lines — the room's talk, thick enough to read at 16px
	line := func(y0 float64, x0, x1 float64) {
		for py := 0; py < size; py++ {
			for px := 0; px < size; px++ {
				x, y := float64(px)/s, float64(py)/s
				if y >= y0 && y < y0+0.035 && x >= x0 && x <= x1 {
					img.Set(px, py, chalkCol)
				}
			}
		}
	}
	line(0.34, 0.26, 0.74)
	line(0.48, 0.26, 0.60)
	line(0.62, 0.26, 0.68)
	// the speaker: a gold head-and-shoulders at the board's right
	head, body := 0.72, 0.78
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			x, y := float64(px)/s, float64(py)/s
			dx, dy := x-head, y-0.60
			if dx*dx+dy*dy <= 0.0036 { // head r=0.06
				img.Set(px, py, speakerCol)
			}
			if y >= body && y <= 0.88 && x >= head-0.09 && x <= head+0.09 {
				img.Set(px, py, speakerCol)
			}
		}
	}
	return img
}

// brandDir locates the checked-in brand assets relative to this
// tool's run (build.sh runs it from the repo root via `go run`).
func brandDir() string {
	for _, p := range []string{"brand/icon-inverted", "../brand/icon-inverted"} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-ico" {
		if err := writeICO(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: icongen <output-iconset-dir> | icongen -ico <output.ico>")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// t_112: the brand assets win when present — copy each size
	// verbatim (the art department's deliverable is the icon, not a
	// starting point to re-draw)
	if bd := brandDir(); bd != "" {
		ok := true
		for _, name := range []string{
			"icon_16x16.png", "icon_16x16@2x.png",
			"icon_32x32.png", "icon_32x32@2x.png",
			"icon_128x128.png", "icon_128x128@2x.png",
			"icon_256x256.png", "icon_256x256@2x.png",
			"icon_512x512.png", "icon_512x512@2x.png",
		} {
			data, err := os.ReadFile(filepath.Join(bd, name))
			if err != nil {
				ok = false
				break
			}
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		if ok {
			fmt.Println("iconset from brand/icon-inverted (t_112 定稿)")
			return
		}
		fmt.Fprintln(os.Stderr, "brand assets incomplete — falling back to the programmatic glyph")
	}
	sizes := []struct {
		name string
		px   int
	}{
		{"icon_16x16.png", 16}, {"icon_16x16@2x.png", 32},
		{"icon_32x32.png", 32}, {"icon_32x32@2x.png", 64},
		{"icon_128x128.png", 128}, {"icon_128x128@2x.png", 256},
		{"icon_256x256.png", 256}, {"icon_256x256@2x.png", 512},
		{"icon_512x512.png", 512}, {"icon_512x512@2x.png", 1024},
	}
	for _, sz := range sizes {
		f, err := os.Create(filepath.Join(dir, sz.name))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := png.Encode(f, draw(sz.px)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("iconset written to %s\n", dir)
}

// icoSizes maps the Windows payload sizes onto the brand iconset
// files: the plain sizes carry over 1:1, and the @2x doubles (which
// exist for Retina) double as the intermediate sizes — 32@2x serves
// 64. 48 is skipped rather than scaled: stdlib has no resampler and
// Windows picks the nearest size up.
var icoSizes = []struct {
	px  int
	src string
}{
	{16, "icon_16x16.png"},
	{32, "icon_32x32.png"},
	{64, "icon_32x32@2x.png"},
	{128, "icon_128x128.png"},
	{256, "icon_256x256.png"},
}

// writeICO assembles a multi-size .ico. Small sizes are packed as
// 32bpp DIBs; the 256px slot embeds the source PNG bytes verbatim
// (the one compression ICO officially sanctions since Vista). Output
// is byte-deterministic for the same inputs — no timestamps in the
// format — so rebuilding never dirties git on a committed artifact.
func writeICO(out string) error {
	bd := brandDir()
	var dirs []byte
	var blobs [][]byte
	var count int
	for _, s := range icoSizes {
		var data []byte
		if bd != "" {
			raw, err := os.ReadFile(filepath.Join(bd, s.src))
			if err == nil {
				img, err := png.Decode(bytes.NewReader(raw))
				if err != nil {
					return fmt.Errorf("brand asset %s is not a PNG: %w", s.src, err)
				}
				if img.Bounds().Dx() != s.px || img.Bounds().Dy() != s.px {
					return fmt.Errorf("brand asset %s is %dx%d, want %dx%d",
						s.src, img.Bounds().Dx(), img.Bounds().Dy(), s.px, s.px)
				}
				if s.px == 256 {
					data = raw // PNG bytes verbatim — Vista+ shortcut for the big slot
				} else {
					data = dib(img)
				}
			}
		}
		if data == nil { // no brand assets — the chalkboard glyph stands in
			data = dib(draw(s.px))
		}
		var d icoDirEntry
		d.setDimensions(s.px)
		d.planes = 1
		d.bitCount = 32
		d.bytesInRes = uint32(len(data))
		offset := uint32(6 + 16*(len(icoSizes))) // directory is fixed-size
		for _, prev := range blobs {             // running sum of prior blobs
			offset += uint32(len(prev))
		}
		d.imageOffset = offset
		dirs = append(dirs, d.encode()...)
		blobs = append(blobs, data)
		count++
	}
	if bd != "" {
		fmt.Println("ico from brand/icon-inverted (t_112 定稿)")
	} else {
		fmt.Println("ico from the programmatic glyph (brand assets absent)")
	}
	buf := make([]byte, 6, 6+16*count)
	binary.LittleEndian.PutUint16(buf[0:], 0) // reserved
	binary.LittleEndian.PutUint16(buf[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(buf[4:], uint16(count))
	buf = append(buf, dirs...)
	for _, b := range blobs {
		buf = append(buf, b...)
	}
	return os.WriteFile(out, buf, 0o644)
}

// icoDirEntry is the 16-byte ICONDIR entry, one per image in the file.
type icoDirEntry struct {
	width, height byte
	colorCount    byte
	reserved      byte
	planes        uint16
	bitCount      uint16
	bytesInRes    uint32
	imageOffset   uint32
}

func (d *icoDirEntry) setDimensions(px int) {
	if px >= 256 { // 0 means 256 in the packed byte fields
		d.width, d.height = 0, 0
	} else {
		d.width, d.height = byte(px), byte(px)
	}
}

func (d *icoDirEntry) encode() []byte {
	b := make([]byte, 16)
	b[0], b[1] = d.width, d.height
	b[2], b[3] = d.colorCount, d.reserved
	binary.LittleEndian.PutUint16(b[4:], d.planes)
	binary.LittleEndian.PutUint16(b[6:], d.bitCount)
	binary.LittleEndian.PutUint32(b[8:], d.bytesInRes)
	binary.LittleEndian.PutUint32(b[12:], d.imageOffset)
	return b
}

// dib packs an image as the ICO's BMP payload: BITMAPINFOHEADER with
// biHeight doubled (pixel rows + AND-mask rows, both bottom-up),
// 32bpp BGRA pixels, then a zeroed AND mask — the alpha channel does
// the actual masking, the mask is vestigial zeros.
func dib(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	row := w * 4
	maskRow := ((w + 31) / 32) * 4
	out := make([]byte, 40+(row+maskRow)*h)
	binary.LittleEndian.PutUint32(out[0:], 40)        // biSize
	binary.LittleEndian.PutUint32(out[4:], uint32(w)) // biWidth
	binary.LittleEndian.PutUint32(out[8:], uint32(h*2))
	binary.LittleEndian.PutUint16(out[12:], 1)  // biPlanes
	binary.LittleEndian.PutUint16(out[14:], 32) // biBitCount
	binary.LittleEndian.PutUint32(out[20:], uint32((row+maskRow)*h))
	for y := 0; y < h; y++ {
		dst := 40 + (h-1-y)*row // bottom-up
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out[dst+x*4+0] = byte(bl >> 8)
			out[dst+x*4+1] = byte(g >> 8)
			out[dst+x*4+2] = byte(r >> 8)
			out[dst+x*4+3] = byte(a >> 8)
		}
	}
	return out
}
