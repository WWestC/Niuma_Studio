// roomdump renders the office scene to PNGs for art inspection —
// a dev tool, not shipped: the layered composite with mock sitters and
// away-facing chairs y-sorted in ONE pass (as the canvas board draws),
// plus 2× zooms of the two islands.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"

	"github.com/WWestC/Niuma_Studio/pixart"
)

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

// zoom2 nearest-neighbor doubles a sub-rect of img.
func zoom2(img *image.NRGBA, r image.Rectangle) *image.NRGBA {
	out := pixart.NewCanvas(r.Dx()*2, r.Dy()*2)
	for y := 0; y < r.Dy()*2; y++ {
		for x := 0; x < r.Dx()*2; x++ {
			out.SetNRGBA(x, y, img.NRGBAAt(r.Min.X+x/2, r.Min.Y+y/2))
		}
	}
	return out
}

// alphaOver composites src onto dst at (0,0) with draw.Over semantics
// (the canvas's drawImage; pixart.Blit is a raw pixel copy).
func alphaOver(dst, src *image.NRGBA) {
	r := src.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			s := src.NRGBAAt(x, y)
			if s.A == 0 {
				continue
			}
			d := dst.NRGBAAt(x, y)
			a := int(s.A)
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8((int(s.R)*a + int(d.R)*(255-a)) / 255),
				G: uint8((int(s.G)*a + int(d.G)*(255-a)) / 255),
				B: uint8((int(s.B)*a + int(d.B)*(255-a)) / 255),
				A: uint8(a + int(d.A)*(255-a)/255),
			})
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: roomdump OUTDIR")
		os.Exit(1)
	}
	dir := os.Args[1]

	back := pixart.BuildRoomBack()
	fore := pixart.BuildRoomFore()
	room := pixart.NewCanvas(pixart.RoomW, pixart.RoomH)
	alphaOver(room, back)

	// mock sitters exactly where the canvas board paints actors: after
	// the back layer, before the fore layer
	atlas := pixart.AvatarAtlas("#d95f4e", "#4a3b2a")
	atlas2 := pixart.AvatarAtlas("#5b8bbd", "#22252a")
	atlas3 := pixart.AvatarAtlas("#34c724", "#6a4a2f")
	fw, fh := pixart.AvatarW, pixart.AvatarH
	frame := func(a *image.NRGBA, i int) *image.NRGBA {
		return a.SubImage(image.Rect(i*fw, 0, (i+1)*fw, fh)).(*image.NRGBA)
	}
	// (x, y) is the sprite's feet anchor — the canvas draws the atlas
	// centered on x, bottom on y
	paste := func(src *image.NRGBA, x, y int) {
		r := src.Bounds()
		for yy := 0; yy < r.Dy(); yy++ {
			for xx := 0; xx < r.Dx(); xx++ {
				s := src.NRGBAAt(r.Min.X+xx, r.Min.Y+yy)
				if s.A == 0 {
					continue
				}
				d := room.NRGBAAt(x+xx, y+yy)
				ai := int(s.A)
				room.SetNRGBA(x+xx, y+yy, color.NRGBA{
					R: uint8((int(s.R)*ai + int(d.R)*(255-ai)) / 255),
					G: uint8((int(s.G)*ai + int(d.G)*(255-ai)) / 255),
					B: uint8((int(s.B)*ai + int(d.B)*(255-ai)) / 255),
					A: uint8(ai + int(d.A)*(255-ai)/255),
				})
			}
		}
	}
	blit := func(a *image.NRGBA, i, x, y int) { paste(frame(a, i), x-fw/2, y-fh) }
	seats := pixart.Seats()

	// every body and away-facing chair goes through one y-sorted pass —
	// the canvas board's contract: a chair covers its own sitter (feet
	// at the spot) yet yields to anyone south of it
	type item struct {
		y  int
		fn func()
	}
	items := []item{}
	at := func(y int, fn func()) { items = append(items, item{y, fn}) }
	avatarAt := func(a *image.NRGBA, i, x, y int) { at(y, func() { blit(a, i, x, y) }) }
	avatarAt(atlas, 0, seats[0].Spot().X, seats[0].Spot().Y)  // face-down sitter
	avatarAt(atlas2, 0, seats[1].Spot().X, seats[1].Spot().Y) // face-down sitter
	avatarAt(atlas, 3, seats[2].Spot().X, seats[2].Spot().Y)  // face-up sitter
	avatarAt(atlas2, 3, seats[3].Spot().X, seats[3].Spot().Y) // face-up sitter
	avatarAt(atlas3, 5, 480, 250)                             // walker crossing the floor
	avatarAt(atlas3, 0, 92, 237)                              // walker just south of a chair — must occlude it
	avatarAt(atlas2, 0, seats[8].Spot().X, seats[8].Spot().Y) // bank-2 sitters
	avatarAt(atlas, 3, seats[10].Spot().X, seats[10].Spot().Y)
	avatarAt(atlas3, 0, seats[13].Spot().X, seats[13].Spot().Y)
	avatarAt(atlas, 3, seats[15].Spot().X, seats[15].Spot().Y)
	chair := pixart.ChairSprite()
	cdx, cdy, _, chh := pixart.ChairSpritePlacement()
	for _, st := range seats {
		if st.Face != pixart.FaceUp {
			continue
		}
		spot := st.Spot()
		at(spot.Y+cdy+chh, func() { paste(chair, spot.X+cdx, spot.Y+cdy) })
	}
	for _, ms := range pixart.MeetingSeats() {
		if ms.Face != pixart.FaceUp {
			continue
		}
		at(ms.Spot.Y+cdy+chh, func() { paste(chair, ms.Spot.X+cdx, ms.Spot.Y+cdy) })
	}
	sort.Slice(items, func(i, j int) bool { return items[i].y < items[j].y })
	for _, it := range items {
		it.fn()
	}

	// fore over everyone — the occlusion pass
	alphaOver(room, fore)
	save(room, dir+"/composite.png")
	save(zoom2(room, image.Rect(30, 90, 390, 230)), dir+"/islands-zoom.png")
	save(zoom2(room, image.Rect(30, 200, 390, 340)), dir+"/bank2-zoom.png")
}
