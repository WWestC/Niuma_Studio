// wardrobedump renders the wardrobe asset library (t_128) to contact
// sheets for art inspection — a dev tool, not shipped. One PNG per
// category, every asset rendered on the tee/straight baseline with a
// pixel label above, big enough to eyeball at a glance:
//
//	wardrobedump outdir
//	  hairstyles.png  all 52 hairstyles × 3 views (down/up/right)
//	  tops.png        100 tops (10 styles × 10 colors)
//	  bottoms.png     50 pants (10 × 5) + 10 skirts
//	  accessories.png 52 accessories on down/right views
//	  haircolors.png  the 10 hair colors
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/WWestC/Niuma_Studio/pixart"
)

const (
	cellW  = 20 // grid px per cell: 16 sprite + 4 margin
	labelH = 7  // label band rows (grid px) above each sprite
	cellH  = 24 + labelH
)

var labelFont = map[rune][]string{
	// 3×5 pixel glyphs, digits 0–9 and a few letters (k for thousand-less
	// compact ids: 0–99 fits two digits)
	'0': {"111", "101", "101", "101", "111"},
	'1': {"010", "110", "010", "010", "111"},
	'2': {"111", "001", "111", "100", "111"},
	'3': {"111", "001", "111", "001", "111"},
	'4': {"101", "101", "111", "001", "001"},
	'5': {"111", "100", "111", "001", "111"},
	'6': {"111", "100", "111", "101", "111"},
	'7': {"111", "001", "010", "010", "010"},
	'8': {"111", "101", "111", "101", "111"},
	'9': {"111", "101", "111", "001", "111"},
	'-': {"000", "000", "111", "000", "000"},
	'F': {"111", "100", "111", "100", "100"},
	'G': {"111", "100", "101", "101", "111"},
	'R': {"111", "101", "111", "110", "101"},
	'B': {"110", "101", "110", "101", "110"},
	'W': {"101", "101", "111", "101", "101"},
	'K': {"101", "101", "110", "101", "101"},
	'C': {"111", "100", "100", "100", "111"},
	'P': {"111", "101", "111", "100", "100"},
	'S': {"111", "100", "111", "001", "111"},
}

// drawLabel paints s (≤4 runes) at grid-px (x, y) on the canvas.
func drawLabel(img *image.NRGBA, x, y int, s string, c color.RGBA) {
	cx := x
	for _, r := range s {
		g, ok := labelFont[r]
		if !ok {
			cx += 4
			continue
		}
		for gy, row := range g {
			for gx, ch := range row {
				if ch == '1' {
					pixart.FillRect(img, (cx+gx)*pixart.S, (y+gy)*pixart.S, pixart.S, pixart.S, c)
				}
			}
		}
		cx += 4
	}
}

func save(img image.Image, path string) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
	fmt.Println("wrote", path)
}

// frame slices frame i (AtlasOrder index) out of an atlas row.
func frame(a *image.NRGBA, i int) *image.NRGBA {
	fw, fh := pixart.AvatarW, pixart.AvatarH
	return a.SubImage(image.Rect(i*fw, 0, (i+1)*fw, fh)).(*image.NRGBA)
}

// pasteCell draws one sprite (a view image) with a label, at grid cell
// (col, row) — cell coords in sprite-grid units, scaled by pixart.S.
func pasteCell(sheet *image.NRGBA, col, row int, sprite *image.NRGBA, label string, lc color.RGBA) {
	x := (col*cellW + 2) * pixart.S
	y := (row*cellH + labelH) * pixart.S
	pixart.Blit(sheet, sprite, x, y)
	drawLabel(sheet, col*cellW+2, row*cellH+1, label, lc)
}

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: wardrobedump OUTDIR")
		os.Exit(1)
	}
	dir := os.Args[1]
	base := pixart.ComposeSpec{
		Skin: "#f0c8a0", Hair: "#3b2f2f", HairStyle: "standard",
		Top: "tee", TopColor: "#3370ff",
		Bottom: "straight", BottomClr: "#2f343c",
	}
	white := color.RGBA{R: 240, G: 240, B: 240, A: 255}

	// --- hairstyles: 52 × 3 views, 13 per row ---
	{
		perRow := 13
		rows := (len(pixart.HairStyles)*3 + perRow - 1) / perRow
		sheet := pixart.NewCanvas(perRow*cellW*pixart.S, rows*cellH*pixart.S)
		i := 0
		for _, h := range pixart.HairStyles {
			sp := base
			sp.HairStyle = h.Key
			set := pixart.ComposeAvatarSet(sp)
			views := []*image.NRGBA{set.DownA, set.UpA, set.RightA}
			for _, v := range views {
				pasteCell(sheet, i%perRow, i/perRow, v, fmt.Sprintf("F%d", i/3+1), white)
				i++
			}
		}
		save(sheet, dir+"/hairstyles.png")
	}

	// --- tops: 100 (10 styles × 10 colors), 20 per row ---
	{
		perRow := 20
		n := len(pixart.TopStyles) * len(pixart.TopColors)
		rows := (n + perRow - 1) / perRow
		sheet := pixart.NewCanvas(perRow*cellW*pixart.S, rows*cellH*pixart.S)
		i := 0
		for si, ts := range pixart.TopStyles {
			for ci, c := range pixart.TopColors {
				sp := base
				sp.Top, sp.TopColor = ts.Key, c
				pasteCell(sheet, i%perRow, i/perRow, pixart.ComposeAvatarSet(sp).DownA,
					fmt.Sprintf("%d-%d", si+1, ci+1), white)
				i++
			}
		}
		save(sheet, dir+"/tops.png")
	}

	// --- bottoms: 50 pants + 10 skirts, 20 per row ---
	{
		perRow := 20
		n := len(pixart.BottomStyles)*len(pixart.BottomColors) + len(pixart.Skirts)
		rows := (n + perRow - 1) / perRow
		sheet := pixart.NewCanvas(perRow*cellW*pixart.S, rows*cellH*pixart.S)
		i := 0
		for si, bs := range pixart.BottomStyles {
			for ci, c := range pixart.BottomColors {
				sp := base
				sp.Bottom, sp.BottomClr = bs.Key, c
				pasteCell(sheet, i%perRow, i/perRow, pixart.ComposeAvatarSet(sp).DownA,
					fmt.Sprintf("P%d-%d", si+1, ci+1), white)
				i++
			}
		}
		for si, sk := range pixart.Skirts {
			sp := base
			sp.Bottom, sp.BottomClr = sk.Key, sk.Color
			pasteCell(sheet, i%perRow, i/perRow, pixart.ComposeAvatarSet(sp).DownA,
				fmt.Sprintf("S%d", si+1), white)
			i++
		}
		save(sheet, dir+"/bottoms.png")
	}

	// --- accessories: 52 × down+right views, 13 per row ---
	{
		perRow := 13
		rows := (len(pixart.Accessories)*2 + perRow - 1) / perRow
		sheet := pixart.NewCanvas(perRow*cellW*pixart.S, rows*cellH*pixart.S)
		i := 0
		for _, a := range pixart.Accessories {
			sp := base
			sp.Acc = []string{a.Key}
			set := pixart.ComposeAvatarSet(sp)
			pasteCell(sheet, i%perRow, i/perRow, set.DownA, fmt.Sprintf("%d", i/2+1), white)
			i++
			pasteCell(sheet, i%perRow, i/perRow, set.RightA, "", white)
			i++
		}
		save(sheet, dir+"/accessories.png")
	}

	// --- hair colors: 10 colors on standard + long ---
	{
		perRow := 10
		sheet := pixart.NewCanvas(perRow*cellW*pixart.S, 2*cellH*pixart.S)
		for i, hc := range pixart.HairColors {
			sp := base
			sp.Hair = hc.Hex
			pasteCell(sheet, i, 0, pixart.ComposeAvatarSet(sp).DownA, fmt.Sprintf("%d", i+1), white)
			sp.HairStyle = "long"
			pasteCell(sheet, i, 1, pixart.ComposeAvatarSet(sp).DownA, "", white)
		}
		save(sheet, dir+"/haircolors.png")
	}
}
