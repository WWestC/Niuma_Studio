// navdump renders the office scene with the board's nav blocked cells
// overlaid in translucent red — a dev tool for auditing walkability
// against the art. Mirrors web/room/nav.js exactly: CELL=4, INFLATE=8,
// desks + props + meeting walls/table.
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
	cell    = 4
	inflate = 4
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: navdump OUTDIR")
		os.Exit(1)
	}
	dir := os.Args[1]
	img := pixart.BuildRoom()
	chair := pixart.ChairSprite()
	dx, dy, _, _ := pixart.ChairSpritePlacement()
	for _, st := range pixart.Seats() {
		if st.Face == pixart.FaceUp {
			pixart.Blit(img, chair, st.Spot().X+dx, st.Spot().Y+dy)
		}
	}
	for _, ms := range pixart.MeetingSeats() {
		if ms.Face == pixart.FaceUp {
			pixart.Blit(img, chair, ms.Spot.X+dx, ms.Spot.Y+dy)
		}
	}

	var rects [][4]int
	for _, st := range pixart.Seats() {
		r := st.Block()
		rects = append(rects, [4]int{r.Min.X, r.Min.Y, r.Dx(), r.Dy()})
	}
	mr := pixart.MeetingRoomRect()
	t := pixart.MeetingWallT()
	gap := pixart.MeetingDoorGap()
	w, h := mr.Dx(), mr.Dy()
	rects = append(rects,
		[4]int{mr.Min.X, mr.Min.Y, gap.Min.X - mr.Min.X, t},
		[4]int{gap.Max.X, mr.Min.Y, mr.Max.X - gap.Max.X, t},
		[4]int{mr.Min.X, mr.Max.Y - t, w, t},
		[4]int{mr.Min.X, mr.Min.Y + t, t, h - t*2},
		[4]int{mr.Max.X - t, mr.Min.Y + t, t, h - t*2},
		mtRect(),
	)

	blocked := image.NewRGBA(image.Rect(0, 0, pixart.RoomW, pixart.RoomH))
	red := color.RGBA{R: 255, A: 110}
	for _, r := range rects {
		x, y, rw, rh := r[0]-inflate, r[1]-inflate, r[2]+inflate*2, r[3]+inflate*2
		for yy := y; yy < y+rh; yy++ {
			for xx := x; xx < x+rw; xx++ {
				if xx < 0 || yy < 0 || xx >= pixart.RoomW || yy >= pixart.RoomH {
					continue
				}
				blocked.SetRGBA(xx, yy, red)
			}
		}
	}
	for yy := 0; yy < pixart.RoomH; yy++ {
		for xx := 0; xx < pixart.RoomW; xx++ {
			b := blocked.RGBAAt(xx, yy)
			if b.A == 0 {
				continue
			}
			d := img.NRGBAAt(xx, yy)
			a := int(b.A)
			img.SetNRGBA(xx, yy, color.NRGBA{
				R: uint8((int(b.R)*a + int(d.R)*(255-a)) / 255),
				G: uint8((int(b.G)*a + int(d.G)*(255-a)) / 255),
				B: uint8((int(b.B)*a + int(d.B)*(255-a)) / 255),
				A: 255,
			})
		}
	}

	save(img, dir+"/nav-overlay.png")
	fmt.Println("wrote", dir+"/nav-overlay.png")
}

func mtRect() [4]int {
	r := pixart.MeetingTableRect()
	return [4]int{r.Min.X, r.Min.Y, r.Dx(), r.Dy()}
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
}
