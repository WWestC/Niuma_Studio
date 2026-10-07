package server

// art.go — the pixel office's asset face (v2 P6): the art layers,
// re-rendered by pixart into plain NRGBA and shipped as PNG for the
// app window's canvas board (web/room/). The office scene is one fixed
// size (pixart.RoomW×RoomH); everything is procedural and
// content-addressed by query, so responses cache hard:
//   GET /art/room-back.png?top=       wall/carpet/furniture behind the actors
//   GET /art/room-fore.png            the transparent desk fronts over the actors
//   GET /art/chair.png                the one task chair as a standalone sprite —
//                                     the board blits one per away-facing seat,
//                                     y-sorted among the actors (a static fore
//                                     layer would paint it over anyone walking
//                                     south of it)
//   GET /art/avatar.png?shirt=&hair=  one member's 9-frame atlas (pixart.AtlasOrder)
//   GET /art/geom.json                desk/lamp/chair rects + seat anchors —
//                                     the same geometry the art was painted with
// The office has no computers: a working desk is told by its lamp,
// which the canvas board repaints lit (warm shade + breathing halo)
// over the unlit brass the back layer paints. The layers' drawing and
// the seating board's hit geometry stay one truth: every rect here is
// derived from pixart's own layout funcs.

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"sync"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/pixart"
)

// artCacheCap bounds the memo table. The scene is fixed-size, so the
// table stays tiny (a few layers + one atlas per member color pair);
// overflowing drops the whole table and entries rebuild on demand.
const artCacheCap = 256

var (
	artCache   sync.Map // key string → artEntry{body, ctype}
	artCacheMu sync.Mutex
)

type artEntry struct {
	body  []byte
	ctype string
}

// artCached memoizes build() by key (content-addressed responses).
func artCached(key string, ctype string, build func() []byte) artEntry {
	if v, ok := artCache.Load(key); ok {
		return v.(artEntry)
	}
	e := artEntry{body: build(), ctype: ctype}
	artCacheMu.Lock()
	if n := artCacheLen(); n >= artCacheCap {
		artCache.Range(func(k, _ any) bool { artCache.Delete(k); return true })
	}
	artCache.Store(key, e)
	artCacheMu.Unlock()
	return e
}

func artCacheLen() int {
	n := 0
	artCache.Range(func(_, _ any) bool { n++; return true })
	return n
}

func writeArt(w http.ResponseWriter, key, ctype string, build func() []byte) {
	e := artCached(key, ctype, build)
	w.Header().Set("Content-Type", e.ctype)
	// 进程内的 artCache 才是内容寻址层；URL 里的 query 只含参数不含美术
	// 版本——换二进制（改像素画）后同 URL 字节会变，max-age 硬缓存会让
	// 浏览器在重启后最多吃一小时旧图（奖杯悬空事故）。改 always-revalidate：
	// 本机回环，校验成本可忽略。
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(e.body)
}

func pngBytes(img *image.NRGBA) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// mountArt wires the /art/ subtree — unconditional (the room art does
// not depend on the WebFS embed).
func (s *Server) mountArt(mux *http.ServeMux) {
	mux.HandleFunc("/art/room-back.png", s.handleArtRoomBack)
	mux.HandleFunc("/art/room-fore.png", s.handleArtRoomFore)
	mux.HandleFunc("/art/chair.png", s.handleArtChair)
	mux.HandleFunc("/art/avatar.png", s.handleArtAvatar)
	mux.HandleFunc("/art/geom.json", s.handleArtGeom)
	mux.HandleFunc("/art/foods.json", s.handleArtFoods)
}

func (s *Server) handleArtRoomBack(w http.ResponseWriter, r *http.Request) {
	// the wall's ceiling fade lerps down from the page chrome's color
	// (the web board passes --titlebar); default = pixart's own
	top := pixart.C(pixart.DefaultCeilTop)
	qtop := ""
	if v := sanitizeHex(r.URL.Query().Get("top"), ""); v != "" {
		if c, ok := hexToRGBA(v); ok {
			top = c
			qtop = v
		}
	}
	// the curtain wall's view phase — the client derives it from the
	// real clock (窗外跟着真实时间走：白天/黄昏/黑夜/黎明); day is
	// the default for callers that don't say
	tod := pixart.ParseTod(r.URL.Query().Get("tod"))
	qtod := [...]string{"day", "dusk", "night", "dawn"}[tod]
	writeArt(w, "room-back?top="+qtop+"&tod="+qtod, "image/png", func() []byte {
		return pngBytes(pixart.BuildRoomBackTod(top, tod))
	})
}

func (s *Server) handleArtRoomFore(w http.ResponseWriter, r *http.Request) {
	writeArt(w, "room-fore", "image/png", func() []byte {
		return pngBytes(pixart.BuildRoomFore())
	})
}

// handleArtChair serves the one task chair as a standalone sprite — the
// board blits one per away-facing seat (pods' near row, meeting south
// row), y-sorted among the actors.
func (s *Server) handleArtChair(w http.ResponseWriter, r *http.Request) {
	writeArt(w, "chair", "image/png", func() []byte {
		return pngBytes(pixart.ChairSprite())
	})
}

// handleArtAvatar serves one member's 9-frame atlas row — frames in
// pixart.AtlasOrder, each AvatarW wide. Unknown/invalid colors fall
// back to the house defaults so a malformed URL still paints someone.
func (s *Server) handleArtAvatar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// 分层签名（r_05，t_126/t_127）：?who=成员名&day=YYYY-MM-DD ——
	// 终身层查 staffing 档案（真源唯一），每日层上衣按日期哈希从
	// 衣柜确定性抽一件（同人同日同衣，重启不重掷）。无 who 参数＝
	// 旧两维签名，逐字节等于现行为（兼容铁律）。
	who := q.Get("who")
	if who == "" {
		shirt := sanitizeHex(q.Get("shirt"), "#7fb0d8")
		hair := sanitizeHex(q.Get("hair"), "#4a3b2a")
		writeArt(w, shirt+"|"+hair, "image/png", func() []byte {
			return pngBytes(pixart.AvatarAtlas(shirt, hair))
		})
		return
	}
	day := q.Get("day")
	spec := s.avatarSpecFor(who, day)
	writeArt(w, "who="+who+"&day="+day+"&look="+spec.HairStyle+"/"+spec.Skin+"/"+spec.Shirt, "image/png", func() []byte {
		return pngBytes(pixart.AvatarAtlasSpec(spec))
	})
}

// wardrobe 是每日层衣柜（r_05 §一）——t_128 并轨后从 5 件纯色起步量
// 扩为全量：10 版型 × 10 色的笛卡尔积（pixart.TopStyles × TopColors），
// 「件」＝版型+颜色的组合，dayHash 对组合数取模。裤/裙随上衣成套
// （§一「套装概念」）：每个上衣组合锚定一条下装，同一索引确定性派生。
type wardrobeItem struct {
	Top    string // 上衣版型键（pixart.TopStyles）
	Color  string // 上衣主色（pixart.TopColors）
	Bottom string // 成套下装键（裤版型或裙款）
	BotClr string // 下装色
}

var wardrobe = buildWardrobe()

// buildWardrobe expands the style×color cartesian product into the daily
// closet. The bottom pairing walks pants styles first (deterministic by
// index) so the same top always rides the same bottom set.
func buildWardrobe() []wardrobeItem {
	var items []wardrobeItem
	i := 0
	for _, ts := range pixart.TopStyles {
		for _, c := range pixart.TopColors {
			item := wardrobeItem{Top: ts.Key, Color: c}
			// 成套下装：裤版型轮换（jogger/straight/wide 日常三主力
			// 居前），色从 BottomColors 走暗色三连（深灰/藏青/卡其）
			pants := []string{"straight", "jogger", "wide"}
			botClr := []string{"#2f343c", "#33415c", "#b09a6b"}
			if i%7 == 3 { // 每 7 件掺一件裙款，办公室不至于全员长裤
				sk := pixart.Skirts[i%len(pixart.Skirts)]
				item.Bottom, item.BotClr = sk.Key, sk.Color
			} else {
				item.Bottom = pants[i%len(pants)]
				item.BotClr = botClr[i%len(botClr)]
			}
			items = append(items, item)
			i++
		}
	}
	return items
}

// avatarSpecFor 合成一名成员的分层 spec：终身层（发型/肤色/饰品）读
// staffing 档案（无档退回标准型——观感即旧款），发色走 Member 的名字
// 哈希（与 join 帧同源），上衣按 day 确定性哈希抽衣柜（无 day＝衣柜
// 首件，兼容不换装的旧调用）。
func (s *Server) avatarSpecFor(who, day string) pixart.AvatarSpec {
	spec := pixart.AvatarSpec{
		Shirt: chat.MemberColor(who),
		Hair:  chat.MemberHair(who),
	}
	if s.opts.Stores.StaffStore != nil {
		if e, ok := s.opts.Stores.StaffStore.Get(chat.LobbyKey, who); ok && e.Look != nil {
			spec.HairStyle = e.Look.HairStyle
			spec.Skin = e.Look.Skin
			for _, a := range e.Look.Accessories {
				spec.Acc = append(spec.Acc, pixart.Accessory{Slot: a.Slot, Style: a.Style, Color: a.Color})
			}
		}
	}
	if day != "" {
		it := wardrobe[int(dayHash(who, day))%len(wardrobe)]
		spec.Shirt = it.Color
		spec.TopStyle = it.Top
		spec.Pants = it.BotClr
		spec.BottomStyle = it.Bottom
	}
	return spec
}

// dayHash 是每日换装的确定性哈希：同人同日同衣（重启不重掷、不闪变）。
func dayHash(who, day string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(who); i++ {
		h ^= uint32(who[i])
		h *= 16777619
	}
	for i := 0; i < len(day); i++ {
		h ^= uint32(day[i])
		h *= 16777619
	}
	return h
}

// sanitizeHex accepts #rgb/#rrggbb (case-insensitive, with or without
// '#') or returns the fallback.
func sanitizeHex(v, fallback string) string {
	if len(v) == 4 && v[0] == '#' {
		return v
	}
	if len(v) == 7 && v[0] == '#' {
		return v
	}
	if len(v) == 3 || len(v) == 6 {
		return "#" + v
	}
	return fallback
}

// hexToRGBA parses sanitizeHex's output (#rgb/#rrggbb) into a color.
func hexToRGBA(hex string) (c color.RGBA, ok bool) {
	s := hex[1:]
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
}

// artGeom is the /art/geom.json payload: the layout truth the canvas
// board seats and hit-tests against. Numbers are native px at the
// scene's one fixed size — the same size the PNG layers rendered at.
type artGeom struct {
	W          int       `json:"w"`
	H          int       `json:"h"`
	WallH      int       `json:"wallH"`
	AvatarW    int       `json:"avatarW"`
	AvatarH    int       `json:"avatarH"`
	Desks      []artSpot `json:"desks"`
	Whiteboard [4]int    `json:"whiteboard"` // x,y,w,h
	Clock      [4]int    `json:"clock"`      // x,y,w,h — the wall clock (click → time panel)
	// ChairSpr is the away-facing chair sprite's blit contract: offset
	// from such a seat's spot (native px) and the sprite's native size.
	// The board y-sorts one blit per face==1 seat (desks and meeting
	// seats alike) among the actors.
	ChairSpr *artChairSpr `json:"chairSpr,omitempty"`
	Meet     *artMeet     `json:"meet,omitempty"`
	Pantry   *artPantry   `json:"pantry,omitempty"`
	Printer  *artPrinter  `json:"printer,omitempty"`
	// Interact（t_105，r_01 §三）：三件可交互物的几何——白板面（写字）、
	// 立式饮水机（倒水）、两株绿植（浇水）。命中与动画共用。
	Interact *artInteract `json:"interact,omitempty"`
	// Props are the freestanding floor props' footprints — the nav grid
	// blocks exactly these, same truth as the layers' art: the rolling
	// whiteboard at the bottom-left standup corner (the pantry's
	// furniture rides Pantry.Props).
	Props [][4]int `json:"props,omitempty"`
}

// artChairSpr is the chair sprite's placement relative to an
// away-facing seat's spot: blit at (spot.x+dx, spot.y+dy), size w×h;
// the sprite's base lands 8 native px below the spot, its sort key.
type artChairSpr struct {
	DX int `json:"dx"`
	DY int `json:"dy"`
	W  int `json:"w"`
	H  int `json:"h"`
}

// artMeet is the meeting room's geometry (需求评审会议): the walls'
// outer rect (the in-room test whose inverse drives the door waypoint),
// the door's outside anchor, the wall display's rect (dark in the art —
// the board paints it lit while a meeting is live), the conference
// table, and the six stand-up places around it.
type artMeet struct {
	Room   [4]int        `json:"room"`   // x,y,w,h of the walls' outer rect
	WallT  int           `json:"wallT"`  // partition thickness
	Door   [2]int        `json:"door"`   // the waypoint outside the door gap
	Gap    [4]int        `json:"gap"`    // the door opening's rect in the north wall
	Screen [4]int        `json:"screen"` // x,y,w,h of the wall display
	Table  [4]int        `json:"table"`  // x,y,w,h of the conference table
	Seats  []artMeetSeat `json:"seats"`
}

// artMeetSeat is one stand-up place: the anchor a participant walks to
// and the facing they hold there (0 faces the camera, 1 faces away).
type artMeetSeat struct {
	Spot [2]int `json:"spot"` // center x, feet y
	Face int    `json:"face"` // 0 面向镜头, 1 背对镜头
}

// artPantry is the 茶水间's geometry: the break corner's zone rect, the
// standing spots idle members take breaks at (face doubles as the
// board's DIR: 0 down, 1 up, 2 east, 3 west), and the furniture
// footprints the nav grid blocks (counter, café table, chairs).
type artPantry struct {
	Rect  [4]int          `json:"rect"`
	Spots []artPantrySpot `json:"spots"`
	Props [][4]int        `json:"props"`
}

// artPantrySpot is one standing place in the pantry.
type artPantrySpot struct {
	Spot [2]int `json:"spot"` // center x, feet y
	Face int    `json:"face"` // 0 面向镜头, 1 背向, 2 面东, 3 面西
}

// artInteract is the t_105 interactive props' geometry: the rolling
// whiteboard's writable face (点击写一行), the standing water dispenser
// (点击倒水) and the two floor plants (点击浇水).
type artInteract struct {
	Board     [4]int   `json:"board"`     // the whiteboard's face rect
	Dispenser [4]int   `json:"dispenser"` // the water dispenser's rect
	Plants    [][4]int `json:"plants"`    // the two plants' rects
}

// artPrinter is the 大型打印机's geometry (r_03): the body's nav
// footprint, the pickup anchor just south of the machine (feet land
// here, facing north at the panel), and the three canvas-paint rects —
// the output tray (the printed sheet slides out over it), the status
// LED (dark in art; lit brand-blue while printing) and the control
// panel band (its highlight pulses while printing).
type artPrinter struct {
	Rect  [4]int `json:"rect"`  // x,y,w,h of the body's footprint
	Spot  [2]int `json:"spot"`  // pickup anchor: center x, feet y (south of body)
	Tray  [4]int `json:"tray"`  // x,y,w,h of the output tray
	LED   [4]int `json:"led"`   // x,y,w,h of the status LED
	Panel [4]int `json:"panel"` // x,y,w,h of the control panel band
}

// artSpot is one seat's geometry: the desk's full art rect (the
// sitter's sprite zone), its lamp-shade and chair-bar rects (both
// painting anchors for the canvas — the lamp is where the canvas paints
// the working glow, the chair where the typing shimmer rolls), the
// furniture footprint the nav grid blocks, the standing anchor a seated
// actor walks to, and the sitter's facing (0 faces the camera, 1 faces
// away — the row whose chair backs read to the viewer).
type artSpot struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Lamp  [4]int `json:"lamp"`  // x,y,w,h (native, absolute)
	Chair [4]int `json:"chair"` // x,y,w,h (native, absolute; empty for camera-facing seats)
	Block [4]int `json:"block"` // x,y,w,h (native, absolute) — the nav grid's collision rect
	Spot  [2]int `json:"spot"`  // seat anchor: desk center x, seated feet y
	Face  int    `json:"face"`  // 0 面向镜头（背靠墙排）, 1 背对镜头
}

// seatGeom derives one workstation's canvas geometry from its seat —
// the exact offsets drawPodBack/drawPodFore paint with (native px
// out). The lamp rect is the seat's own, the same truth the art's
// shade was painted with.
func seatGeom(st pixart.Seat) artSpot {
	lr, cr, br, sp := st.Lamp(), st.ChairRect(), st.Block(), st.Spot()
	return artSpot{
		X: st.Rect.Min.X, Y: st.Rect.Min.Y, W: st.Rect.Dx(), H: st.Rect.Dy(),
		Lamp:  [4]int{lr.Min.X, lr.Min.Y, lr.Dx(), lr.Dy()},
		Chair: [4]int{cr.Min.X, cr.Min.Y, cr.Dx(), cr.Dy()},
		Block: [4]int{br.Min.X, br.Min.Y, br.Dx(), br.Dy()},
		Spot:  [2]int{sp.X, sp.Y},
		Face:  st.Face,
	}
}

// handleArtFoods serves the food catalog (r_07 t_136): entry keys,
// names, two colors and the hand/residue forms as character grids —
// the board's handheld rendering and the litter's residue shape both
// draw from this one truth.
func (s *Server) handleArtFoods(w http.ResponseWriter, r *http.Request) {
	writeArt(w, "foods", "application/json", func() []byte {
		type foodOut struct {
			Key     string   `json:"key"`
			Name    string   `json:"name"`
			Kind    string   `json:"kind"` // snack|fruit（t_142：按 key 前缀判定落值）
			Hex     string   `json:"hex"`
			Hex2    string   `json:"hex2"`
			Hand    []string `json:"hand"`
			Residue string   `json:"residue"`
			Res     []string `json:"res"`
		}
		out := make([]foodOut, 0, len(pixart.Foods))
		for _, f := range pixart.Foods {
			kind := "fruit"
			pre := f.Key[len("food-"):]
			if (len(pre) > 4 && (pre[:4] == "bag-" || pre[:4] == "can-" || pre[:4] == "box-")) ||
				(len(pre) > 6 && pre[:6] == "stick-") {
				kind = "snack"
			}
			out = append(out, foodOut{Key: f.Key, Name: f.Name, Kind: kind, Hex: f.Hex, Hex2: f.Hex2,
				Hand: f.Hand, Residue: f.Residue, Res: f.ResRows})
		}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(out); err != nil {
			return []byte(`[]`)
		}
		return buf.Bytes()
	})
}

func (s *Server) handleArtGeom(w http.ResponseWriter, r *http.Request) {
	writeArt(w, "geom", "application/json", func() []byte {
		g := artGeom{
			W: pixart.RoomW, H: pixart.RoomH, WallH: pixart.WallH,
			AvatarW: pixart.AvatarW, AvatarH: pixart.AvatarH,
		}
		bb := pixart.WhiteboardRect()
		g.Whiteboard = [4]int{bb.Min.X, bb.Min.Y, bb.Dx(), bb.Dy()}
		ck := pixart.ClockRect()
		g.Clock = [4]int{ck.Min.X, ck.Min.Y, ck.Dx(), ck.Dy()}
		cdx, cdy, cw, ch := pixart.ChairSpritePlacement()
		g.ChairSpr = &artChairSpr{DX: cdx, DY: cdy, W: cw, H: ch}
		for _, st := range pixart.Seats() {
			g.Desks = append(g.Desks, seatGeom(st))
		}
		mr := pixart.MeetingRoomRect()
		ms := pixart.MeetingScreenRect()
		mt := pixart.MeetingTableRect()
		door := pixart.MeetingDoor()
		meet := &artMeet{
			Room:   [4]int{mr.Min.X, mr.Min.Y, mr.Dx(), mr.Dy()},
			WallT:  pixart.MeetingWallT(),
			Door:   [2]int{door.X, door.Y},
			Gap:    func() [4]int { gp := pixart.MeetingDoorGap(); return [4]int{gp.Min.X, gp.Min.Y, gp.Dx(), gp.Dy()} }(),
			Screen: [4]int{ms.Min.X, ms.Min.Y, ms.Dx(), ms.Dy()},
			Table:  [4]int{mt.Min.X, mt.Min.Y, mt.Dx(), mt.Dy()},
		}
		for _, s := range pixart.MeetingSeats() {
			meet.Seats = append(meet.Seats, artMeetSeat{
				Spot: [2]int{s.Spot.X, s.Spot.Y}, Face: s.Face,
			})
		}
		g.Meet = meet
		pz := pixart.PantryRect()
		pantry := &artPantry{
			Rect:  [4]int{pz.Min.X, pz.Min.Y, pz.Dx(), pz.Dy()},
			Spots: make([]artPantrySpot, 0, 5),
		}
		for _, s := range pixart.PantrySpots() {
			pantry.Spots = append(pantry.Spots, artPantrySpot{
				Spot: [2]int{s.Spot.X, s.Spot.Y}, Face: s.Face,
			})
		}
		for _, b := range pixart.PantryBlocks() {
			pantry.Props = append(pantry.Props, [4]int{b.Min.X, b.Min.Y, b.Dx(), b.Dy()})
		}
		g.Pantry = pantry
		for _, pr := range []image.Rectangle{pixart.BoardStandRect()} {
			if pr.Empty() {
				continue
			}
			g.Props = append(g.Props, [4]int{pr.Min.X, pr.Min.Y, pr.Dx(), pr.Dy()})
		}
		if br := pixart.WhiteboardRect(); !br.Empty() {
			dr := pixart.DispenserRect()
			interact := &artInteract{
				Board:     [4]int{br.Min.X, br.Min.Y, br.Dx(), br.Dy()},
				Dispenser: [4]int{dr.Min.X, dr.Min.Y, dr.Dx(), dr.Dy()},
			}
			for _, pr := range pixart.PlantRects() {
				interact.Plants = append(interact.Plants, [4]int{pr.Min.X, pr.Min.Y, pr.Dx(), pr.Dy()})
			}
			g.Interact = interact
		}
		if pr := pixart.PrinterRect(); !pr.Empty() {
			g.Props = append(g.Props, [4]int{pr.Min.X, pr.Min.Y, pr.Dx(), pr.Dy()})
			tr, led, pn := pixart.PrinterTrayRect(), pixart.PrinterLEDRect(), pixart.PrinterPanelRect()
			sp := pixart.PrinterSpot()
			g.Printer = &artPrinter{
				Rect:  [4]int{pr.Min.X, pr.Min.Y, pr.Dx(), pr.Dy()},
				Spot:  [2]int{sp.X, sp.Y},
				Tray:  [4]int{tr.Min.X, tr.Min.Y, tr.Dx(), tr.Dy()},
				LED:   [4]int{led.Min.X, led.Min.Y, led.Dx(), led.Dy()},
				Panel: [4]int{pn.Min.X, pn.Min.Y, pn.Dx(), pn.Dy()},
			}
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		if err := enc.Encode(g); err != nil {
			return []byte(`{"error":"geom encode failed"}`)
		}
		return buf.Bytes()
	})
}
