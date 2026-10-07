package pixart

import (
	"image"
	"image/color"
)

// S is the rescale factor (t_71/t_76): every legacy 384-era rect
// coordinate in this file reads in old units and renders ×S — the art
// keeps its hand-laid proportions while the office doubles, each pixel
// freshly drawn (never a bitmap stretch). New detail work may draw in
// native units via rectN. Loop step values stay un-scaled on purpose:
// denser banding/seams read as more detail, not just bigger.
const S = 2

// RoomW/RoomH are the office scene's one fixed native size — the art's
// hand-laid 384-legacy design width, tall enough for the two island
// banks plus open floor below. Every room asset renders at exactly
// this size; viewers letterbox the scene, never re-lay it out.
const (
	RoomW = 384 * S
	RoomH = 260 * S
)

// WallH is the height of the back wall (rendered px); actors walk below it.
const WallH = 64 * S

// WhiteboardW/H are the whiteboard's pixel size — the board face
// (frame included) the back wall used to wear and the floor stand now
// carries downstairs; same size, same hit rect, only the home moved.
const (
	WhiteboardW = 44 * S
	WhiteboardH = 26 * S
)

// ── the curtain wall (落地窗幕墙) ────────────────────────────────────
//
// The back wall is a CBD tower's curtain wall: two floor-to-ceiling
// panes in slim charcoal mullions flanking a wide solid column (the
// column carries the clock and, at its east end, the pantry's snack
// shelving), and a city skyline outside that follows the real clock —
// day, dusk, night and dawn each repaint the sky, the towers' lit
// windows and the street lights. The client derives the phase from
// local time (room-back.png?tod=); day is the timeless default for
// static callers. The wall-hung whiteboard followed the glass
// downstairs: it now rolls on casters at the office's bottom-left
// standup corner. All legacy-unit rects (rendered ×S).
const (
	paneL0      = 22  // west pane's left jamb
	paneL1      = 172 // west pane's right jamb (the column starts here)
	paneR0      = 266 // east pane's left jamb (east of the snack shelves)
	paneR1      = 356 // east pane's right jamb
	paneBayW    = 30  // mullion pitch — the curtain grid
	paneTop     = 14  // glass begins under the transom
	paneBot     = 58  // glass ends at the base band
	paneHorizon = 50  // the far skyline meets the street here
	paneGroundY = 56  // near towers stand to here (in front of the street)
)

// The curtain wall's view phases — picked from the real clock.
const (
	TodDay = iota
	TodDusk
	TodNight
	TodDawn
)

// ParseTod maps the wire's phase names onto the consts; anything else
// (including "") is day, the static default.
func ParseTod(s string) int {
	switch s {
	case "dusk":
		return TodDusk
	case "night":
		return TodNight
	case "dawn":
		return TodDawn
	default:
		return TodDay
	}
}

// SeatW is one workstation seat's desk width; podSeam the gap between
// the two desks of a row (pushed together, but a seam keeps the two
// seats countable).
const (
	SeatW   = 36 * S
	podSeam = 2 * S
)

// podH is one island's art depth (far chair headrest wing down to the
// near chair's casters); bankPitch leaves a carpet walkway between the
// two banks and open floor below. The depth is deliberate: opposite
// sitters' sprites must not merge on screen, so the near sitter's head
// clears the far sitter's body entirely — the far face reads above the
// near chair, like a real office seen from standing height.
const (
	podH        = 44 * S
	bankPitch   = 70 * S
	bank0Y      = 70 * S // first bank just under the wall
	pod0X       = 26 * S // just clear of the corner plant
	podPitch    = 82 * S // 74 pod + walk gap
	podsPerBank = 2
	banks       = 2
	seatsPerRow = 2
)

// Sitting face — which way a seat's occupant faces: FaceDown looks at
// the camera (the wall-backed row), FaceUp looks away (the row whose
// ergonomic chair backs read to the camera). The two rows of an island
// sit face to face across the pushed-together desks, like a real
// open-plan bench.
const (
	FaceDown = 0
	FaceUp   = 1
)

// Seat is one workstation: its art zone, the sitter's facing, and the
// geometry the canvas board shares (lamp shade / chair back / seat
// anchor). Every rect is native px; the chair rect is empty for
// FaceDown seats (their backrest hides behind the sitter, so the
// board's shimmer has nothing to roll across). Block is the furniture
// footprint — the desk-and-chair band the nav grid blocks — distinct
// from Rect, the sitter's sprite zone: the FaceDown zone starts at the
// seated head line (12 legacy px above the chair), and blocking that
// would wall off the open aisle between islands.
type Seat struct {
	Rect  image.Rectangle
	Face  int
	lamp  image.Rectangle
	chair image.Rectangle
	block image.Rectangle
	spot  image.Point
}

// Lamp is the lamp-shade rect the canvas repaints lit for a working
// desk — one truth with the shade the art draws.
func (s Seat) Lamp() image.Rectangle { return s.lamp }

// ChairRect is the backrest band the canvas shimmers across (empty for
// camera-facing seats).
func (s Seat) ChairRect() image.Rectangle { return s.chair }

// Block is the furniture footprint the nav grid blocks (geom.json's
// per-seat block rect) — what the art painted as desk and chair, not
// the sitter's headroom above it.
func (s Seat) Block() image.Rectangle { return s.block }

// Spot is the anchor a seated actor walks to (desk center x, seated
// feet y).
func (s Seat) Spot() image.Point { return s.spot }

// Pod is one four-seat face-to-face island: two camera-facing desks on
// the wall side, two away-facing desks on the camera side.
type Pod struct {
	X, Y  int     // native origin (island's top-left)
	Seats [4]Seat // upper-left, upper-right, lower-left, lower-right
}

func newSeat(x, py, face int) Seat {
	cx := x + 18*S
	st := Seat{Face: face}
	switch face {
	case FaceDown:
		// far row (wall side): feet at the far desk line, face and
		// chest visible above their own desk (fore)
		st.Rect = image.Rect(x, py-14*S, x+SeatW, py+13*S)
		st.spot = image.Pt(cx, py+12*S)
		// east-end lamp: shade hanging over the work area
		st.lamp = image.Rect(x+26*S, py, x+34*S, py+3*S)
		// furniture only: chair headrest (drawTaskChair's top at
		// py-2 legacy) down to the desk front edge — the 12 legacy px
		// of open floor between the sitter's head line and the
		// headrest stay walkable (the islands' aisle)
		st.block = image.Rect(x, py-2*S, x+SeatW, py+13*S)
	default: // FaceUp
		// near row (camera side): feet low enough that their head
		// (rows 16..26) clears the far sitter (rows -12..12) entirely —
		// they read as a hair crown over their own chair back
		st.Rect = image.Rect(x, py+16*S, x+SeatW, py+44*S)
		st.spot = image.Pt(cx, py+40*S)
		// west-end lamp rising from the desk's far edge, clear of the
		// sitter's head
		st.lamp = image.Rect(x+2*S, py+15*S, x+10*S, py+18*S)
		// the mesh high-back the canvas shimmers across
		st.chair = image.Rect(cx-7*S, py+29*S, cx+7*S, py+37*S)
		// the zone is furniture all the way down (bench spine, near
		// desk, chair sprite)
		st.block = st.Rect
	}
	return st
}

// Pods lays the face-to-face islands on the fixed-size floor: two
// banks of two 2×2 islands each, walkways between and around them.
func Pods() []Pod {
	pods := make([]Pod, 0, banks*podsPerBank)
	for b := 0; b < banks; b++ {
		for p := 0; p < podsPerBank; p++ {
			px, py := pod0X+p*podPitch, bank0Y+b*bankPitch
			pod := Pod{X: px, Y: py}
			for c := 0; c < seatsPerRow; c++ {
				ux := px + c*(SeatW+podSeam)
				pod.Seats[c] = newSeat(ux, py, FaceDown) // wall-backed row
				pod.Seats[2+c] = newSeat(ux, py, FaceUp) // camera-backed row
			}
			pods = append(pods, pod)
		}
	}
	return pods
}

// Seats flattens every island's four seats in layout order (the seat
// pool's index space).
func Seats() []Seat {
	pods := Pods()
	out := make([]Seat, 0, len(pods)*4)
	for _, p := range pods {
		out = append(out, p.Seats[0], p.Seats[1], p.Seats[2], p.Seats[3])
	}
	return out
}

// ClockRect returns the wall clock's rectangle, riding the curtain
// wall's solid center column. Shared by BuildRoomBack and the office
// board's hit-testing (点挂钟弹时间面板) so drawing and hits never drift
// apart.
func ClockRect() image.Rectangle {
	x, y := RoomW/2-7*S, 18*S
	return image.Rect(x, y, x+10*S, y+10*S)
}

// WhiteboardRect returns the whiteboard face's rectangle — the board
// the floor stand carries at the office's bottom-left standup corner
// (the curtain wall took the back wall, the board went downstairs).
// Shared by BuildRoomBack and the office board's hit-testing so
// drawing and hits never drift apart.
func WhiteboardRect() image.Rectangle {
	x, y := 53*S, 204*S
	return image.Rect(x, y, x+WhiteboardW, y+WhiteboardH)
}

// BoardStandRect is the rolling whiteboard's full footprint (face +
// posts + caster base) — the nav grid blocks exactly this, the same
// truth the other floor props keep.
func BoardStandRect() image.Rectangle {
	return image.Rect(49*S, 204*S, 101*S, 247*S)
}

// ── the big printer (大型打印机, r_03) ───────────────────────────────
//
// A floor-standing office multifunction printer flanking the rolling
// whiteboard's east side (design/r03-printer §一): a three-deck body —
// the top deck's output tray with one sheet always lying in it under
// the scanner lid, the middle deck's slanted control panel with a
// small dark screen, status LED and two keys, the bottom deck's paper
// cabinet with slab doors and a kick. The tray's lit LED / sliding
// sheet / paper-in-tray animation is painted by the canvas board (the
// same dark-seat/lit-glow contract the desk lamps keep). All rects
// native px (min/max form), one truth with geom.json's printer block.

var (
	// 坐标系订正（房主验收发现）：r03 设计稿写的「native (103,204)」实为
	// legacy 数值——白板真身 BoardStandRect 是 native (98,408)-(202,494)。
	// 房主二验：放大到与白板同高、底边对齐白板腿（y=494）——大型复合机
	// 的体量。西缘 202+4（2 legacy 缝），宽 40 native（20 legacy）。
	printerRect  = image.Rect(206, 452, 246, 494) // 机身脚印（含底座）；高 42≈半高，底对齐白板腿
	printerSpot  = image.Pt(226, 498)             // 取件锚点：机身南侧走道，面北朝机器
	printerTray  = image.Rect(212, 457, 240, 463) // 出纸托盘（顶段）
	printerLED   = image.Rect(234, 468, 238, 471) // 状态指示灯（中段面板）
	printerPanel = image.Rect(210, 466, 242, 474) // 操作面板带（中段）
)

// PrinterRect returns the body's footprint — the nav grid blocks
// exactly this, same truth as the art.
func PrinterRect() image.Rectangle { return printerRect }

// PrinterSpot returns the pickup anchor just south of the machine.
func PrinterSpot() image.Point { return printerSpot }

// PrinterTrayRect returns the output tray's rect (the canvas board
// slides the printed sheet out over it).
func PrinterTrayRect() image.Rectangle { return printerTray }

// PrinterLEDRect returns the status LED's rect (dark in the art; the
// canvas board paints it lit while printing).
func PrinterLEDRect() image.Rectangle { return printerLED }

// PrinterPanelRect returns the control panel band (the canvas board
// pulses its highlight while printing).
func PrinterPanelRect() image.Rectangle { return printerPanel }

// ── t_105 交互物几何（r_01 §三）：白板面（写字）、立式饮水机（倒水）、
// 两株落地绿植（浇水）——命中测试与画布动画共用一份真源 ──

var (
	// 立式饮水机：左下角老式落地机（drawDispenser legacy (2,52)）
	dispenserRect = image.Rect(4, 104, 24, 160)
	// 两株落地绿植（drawPlant）：西南角＋东墙边
	plantARect = image.Rect(28, 132, 48, 160)
	plantBRect = image.Rect(604, 130, 624, 158)
)

// DispenserRect returns the standing water dispenser's rect (点击倒水).
func DispenserRect() image.Rectangle { return dispenserRect }

// PlantRects returns the two floor plants' rects (点击浇水).
func PlantRects() []image.Rectangle { return []image.Rectangle{plantARect, plantBRect} }

// ── the meeting room (需求评审会议) ──────────────────────────────────
//
// One glass-walled meeting room in the office's south-east corner:
// partition walls with a door gap in the north run, a conference table
// under a wall-mounted display, and six LOW boardroom chairs around
// the table — short enough that the wall screen reads unbroken behind
// the north row (backrest tops 26px below the screen's bottom edge).
// North chairs' bodies ride the back layer (a seated sprite covers
// them), the south row's ride the board's y-sorted ChairSprite in rear
// view (crown above the headrest). The screen ships DARK in the art —
// the office board paints it lit (a breathing review slide) while a
// meeting is live, the same unlit-brass/lit-glow contract the desk
// lamps keep. All rects are native px and one truth with geom.json
// (server/art.go reads the accessors below).

const (
	meetWallT = 6 * S // partition thickness, native
)

// meetRoom is the walls' outer rect (the door gap eats the north
// wall's east end); the floor inside is the scene's own carpet plus a
// rug under the table.
var meetRoom = image.Rect(231*S, 163*S, 374*S, 253*S)

// meetDoorGap is the opening span in the north wall; meetDoorOut is
// the waypoint just outside it (participants file in through here).
var (
	meetDoorGap   = image.Rect(344*S, meetRoom.Min.Y, 369*S, meetRoom.Min.Y+meetWallT)
	meetDoorOut   = image.Pt(356*S, meetRoom.Min.Y-6*S)
	meetScreen    = image.Rect(250*S, 170*S, 328*S, 184*S)
	meetTable     = image.Rect(253*S, 209*S, 327*S, 228*S)
	meetRug       = image.Rect(243*S, 176*S, 362*S, 239*S)
	meetSeatY     = 208 * S // north row's feet (south row sits +27)
	meetSeatDX    = 31 * S  // between-seat pitch
	meetSeatX0    = 259 * S // west-most seat center
	meetSouthDY   = 27 * S
	meetSeatCount = 3 // per row (two rows = six places)
)

// MeetSeat is one chair around the conference table: the anchor a
// participant walks to (their feet land on the chair's seat line) and
// the facing they hold there (0 faces the camera across the table, 1
// faces away).
type MeetSeat struct {
	Spot image.Point
	Face int
}

// MeetingSeats lays the six chairs: the near-screen row (facing the
// camera) then the camera-side row (backs to the lens). The PM takes
// index 0; participants fill the rest in invite order.
func MeetingSeats() []MeetSeat {
	out := make([]MeetSeat, 0, meetSeatCount*2)
	for i := 0; i < meetSeatCount; i++ {
		x := meetSeatX0 + i*meetSeatDX
		out = append(out, MeetSeat{Spot: image.Pt(x, meetSeatY), Face: FaceDown})
	}
	for i := 0; i < meetSeatCount; i++ {
		x := meetSeatX0 + i*meetSeatDX
		out = append(out, MeetSeat{Spot: image.Pt(x, meetSeatY+meetSouthDY), Face: FaceUp})
	}
	return out
}

// MeetingRoomRect returns the walls' outer rect — the board's
// in-room test (whose inverse drives the door waypoint).
func MeetingRoomRect() image.Rectangle { return meetRoom }

// MeetingDoor returns the door's outside waypoint.
func MeetingDoor() image.Point { return meetDoorOut }

// MeetingScreenRect returns the wall display's rect (dark in art; the
// board paints it lit while a meeting is live).
func MeetingScreenRect() image.Rectangle { return meetScreen }

// MeetingTableRect returns the conference table's rect.
func MeetingTableRect() image.Rectangle { return meetTable }

// MeetingWallT returns the partition walls' thickness — the board's
// nav grid builds the four wall runs (door gap excluded) from the room
// rect and this.
func MeetingWallT() int { return meetWallT }

// MeetingDoorGap returns the door opening's rect in the north wall —
// the nav grid's one walkable seam into the room.
func MeetingDoorGap() image.Rectangle { return meetDoorGap }

// ── the pantry (茶水间) ──────────────────────────────────────────────
//
// A long L-shaped pantry counter filling the east half of the floor
// above the meeting room (城市办公楼茶水间): the north arm runs the
// wall's foot east–west nearly wall-to-wall, the east arm rides the
// scene's east edge down toward the meeting room — quartz tops, matte
// flat-panel cabinets (the westmost bay a three-drawer stack), an
// integrated steel fridge wearing round magnets and a sticky note.
// The north arm's worktop carries the coffee machine, gooseneck
// kettle, an inset sink with faucet (洗杯子), a drying mat with mugs,
// a cup dispenser and a red/blue jar pair; overhead open shelving
// holds the snack packets (零食架). Below the east arm's quartz edge
// an open storage unit stacks colorful boxes, paper bags and jars —
// the closed tall doors read as a gray wall, the open cubbies give
// the cool corner some life. The nav grid blocks
// the furniture footprints (geom.pantry.props), and idle members take
// coffee breaks standing at the spots below (geom.pantry.spots) —
// busy members never move (t_103). All rects native px (min/max form),
// one truth with geom.json (server/art.go reads the accessors below).

var (
	pantryZone = image.Rect(440, 160, 768, 320)
	// 北臂（东西横贯）与东臂（贴东缘南下）的 L 型台身；台前有吧台
	// 凳×2、脚踏垃圾桶，台外西端立着饮水机。全部是家具脚印，nav
	// 网格照此挡格
	pantryArmN      = image.Rect(472, 164, 768, 206)
	pantryArmE      = image.Rect(724, 164, 768, 316)
	pantryStool0    = image.Rect(560, 208, 576, 232)
	pantryStool1    = image.Rect(636, 208, 652, 232)
	pantryBin       = image.Rect(488, 206, 506, 232)
	pantryWaterCtlr = image.Rect(450, 172, 476, 220)
)

// PantrySpot is one standing place in the pantry: the anchor a
// break-taking member walks to and the facing they hold there — 0
// faces the camera (down), 1 away (up), 2 east (right), 3 west (left);
// the same ints the web board's DIR keeps.
type PantrySpot struct {
	Spot image.Point
	Face int
}

// PantrySpots lays the five standing places: three along the north
// arm's south face and two along the east arm's west face, all facing
// the counter they drink at.
func PantrySpots() []PantrySpot {
	return []PantrySpot{
		{Spot: image.Pt(520, 236), Face: 1}, // 北臂台前·西
		{Spot: image.Pt(600, 236), Face: 1}, // 北臂台前·中
		{Spot: image.Pt(676, 236), Face: 1}, // 北臂台前·东
		{Spot: image.Pt(704, 264), Face: 2}, // 东臂台前·北
		{Spot: image.Pt(704, 300), Face: 2}, // 东臂台前·南
	}
}

// PantryRect returns the zone's loose rect (the break corner's
// footprint on the floor — informational, nothing blocks on it).
func PantryRect() image.Rectangle { return pantryZone }

// PantryBlocks returns the pantry furniture footprints the nav grid
// blocks — the L counter's two arms and the two bar stools.
func PantryBlocks() []image.Rectangle {
	return []image.Rectangle{pantryArmN, pantryArmE, pantryStool0, pantryStool1,
		pantryBin, pantryWaterCtlr}
}

// The palette tracks the app's current face (飞书风 flat: light walls,
// gray-blue carpet, brand-blue accents) — the pixel office should read
// as the same product the page chrome around it is.
var (
	wallC      = C("#eef0f4") // near-white cool wall
	wallShade  = C("#e2e5ea")
	wainscot   = C("#dbe1e7") // light panel wainscot
	baseboard  = C("#b7c1c9")
	floorA     = C("#bfc9d4") // office carpet tiles, one step darker
	floorB     = C("#bac4cf") // than the wall so the horizon reads
	floorSeam  = C("#b0bbc8")
	floorFleck = C("#a3afc0")
	cloudC     = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	terraC     = C("#b5651d")
	terraD     = C("#8f4e16")
	leafC      = C("#4f7d4a")
	leafD      = C("#3d6339")
	clockFace  = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	metalC     = C("#3b4048")
	// 工位家具：浅橡木桌面＋细金属腿＋钢蓝办公椅——冷灰环境里唯一
	// 的暖色块，和页面主题的品牌蓝同一族
	deskTopC  = C("#e3d5b8")
	deskEdgeC = C("#c6b48e")
	legC      = C("#565d66")
	pedestalC = C("#cfd6dd")
	chairC    = C("#667ea6")
	chairHiC  = C("#8ba3c9")
	chairDkC  = C("#4c5f85")
	mugRed    = C("#d95f4e")
	mugBlue   = C("#5b8bbd")
	paperC    = C("#fbfbf8")
	// whiteboard + office props (the theme's accent family: 品牌蓝/绿/橙/红)
	boardFrameC = C("#aab4bd")
	boardFaceC  = C("#fbfbf8")
	markerBlue  = C("#3370ff")
	markerGreen = C("#34c724")
	markerRed   = C("#f54a45")
	markerGray  = C("#a9b2bb")
	caseBodyC   = C("#dde3e8") // file cabinet
	caseLineC   = C("#b7c1c9")
	dispenserC  = C("#f0f3f6") // water dispenser body
	printerC    = C("#edf0f4")
	printerDark = C("#d9dfe4")
	printerKey  = C("#8f959e")
	// 台灯与桌面小物：无电脑办公室里唯一的自发光，暖黄与页面品牌蓝互为补色
	lampOffC   = C("#d5c69b") // 未点亮的灯罩，黄铜暗面
	lampOffDkC = C("#b9a97e") // 灯罩下缘
	paperShade = C("#e8e6df") // 纸堆的层叠影
	// 会议室（需求评审会议）：玻璃隔断 + 会议桌 + 墙挂大屏——屏幕在
	// 图层里永远熄灭，会议进行时由画布点亮（与台灯同一套暗座/亮晕契约）
	partGlassC   = C("#dfe7ee") // 隔断玻璃
	partEdgeC    = C("#9fadba") // 隔断框线
	partBaseC    = C("#c8d2dc") // 隔断下缘实心带
	rugC         = C("#a8b8cc") // 会议地毯
	rugEdgeC     = C("#93a6bf")
	rugFleckC    = C("#b6c5d8")
	screenBezelC = C("#23282f") // 大屏边框
	screenOffC   = C("#343b44") // 熄屏面
	screenHiC    = C("#3d454f") // 熄屏反光
	screenLedC   = C("#d95f4e") // 待机红灯
	tableFrontC  = C("#d9caa9") // 会议桌立面
	woodC        = C("#a06a3f") // 吧台凳座面——暖木
	woodHiC      = C("#b98a5a")
	// 茶水间（城市办公楼款）：石英白台面＋哑光浅灰平板柜＋嵌入式钢
	// 门冰箱——干净现代，与工位橡木一暖一冷正好分家
	quartzC      = C("#eef1f6") // 石英台面
	quartzEdgeC  = C("#c9d2dc") // 台面阴影线
	pantryCabC   = C("#e2e6eb") // 哑光柜身
	pantryLineC  = C("#cbd1d8") // 平板柜门缝
	pantryKickC  = C("#aab3bc") // 踢脚
	pantrySteelC = C("#cdd4da") // 冰箱钢门
)

// BuildRoom renders the full static office — back and foreground
// composited, with the away-facing chairs blitted at their spots — for
// scene-only snapshots. The live canvas board draws the pieces
// separately: back → actors and chair sprites y-sorted together → fore
// (the busy desks' lamp glow is painted by the canvas too), so seated
// members read as sitting behind their desks while anyone south of a
// chair occludes it. The composite goes through draw.Over (alpha), not
// raw pixel copies — transparent pixels in the upper layers must not
// punch holes in the ones below.
func BuildRoom() *image.NRGBA {
	img := BuildRoomBack()
	over(img, BuildRoomFore())
	chair := ChairSprite()
	dx, dy, _, _ := ChairSpritePlacement()
	for _, st := range Seats() {
		if st.Face == FaceUp {
			Blit(img, chair, st.Spot().X+dx, st.Spot().Y+dy)
		}
	}
	for _, ms := range MeetingSeats() {
		if ms.Face == FaceUp {
			Blit(img, chair, ms.Spot.X+dx, ms.Spot.Y+dy)
		}
	}
	return img
}

// over alpha-composites src onto dst at (0,0) — BuildRoom's layer join.
func over(dst, src *image.NRGBA) {
	r := src.Bounds()
	for yy := r.Min.Y; yy < r.Max.Y && yy < dst.Rect.Max.Y; yy++ {
		for xx := r.Min.X; xx < r.Max.X && xx < dst.Rect.Max.X; xx++ {
			s := src.NRGBAAt(xx, yy)
			if s.A == 0 {
				continue
			}
			if s.A == 255 {
				dst.SetNRGBA(xx, yy, s)
				continue
			}
			d := dst.NRGBAAt(xx, yy)
			a := int(s.A)
			dst.SetNRGBA(xx, yy, color.NRGBA{
				R: uint8((int(s.R)*a + int(d.R)*(255-a)) / 255),
				G: uint8((int(s.G)*a + int(d.G)*(255-a)) / 255),
				B: uint8((int(s.B)*a + int(d.B)*(255-a)) / 255),
				A: uint8(a + int(d.A)*(255-a)/255),
			})
		}
	}
}

// BuildRoomBack renders the office layer that sits behind the actors:
// wall, carpet, the curtain wall's city view (day phase), clock, the
// rolling whiteboard downstairs, plant, the file
// cabinet, water dispenser, printer, the islands' rear halves (the
// camera-facing row's chair rears, the away-facing row's desk surfaces
// and props, the bench spines). The
// wall's top fades from the app chrome's color
// (defaultCeilTop) into the wall, so the canvas edge melts into the
// page above it.
func BuildRoomBack() *image.NRGBA {
	return BuildRoomBackTop(C(DefaultCeilTop))
}

// DefaultCeilTop tracks the web theme's --titlebar default (the 飞书风
// light strip); /art callers may override via ?top=.
const DefaultCeilTop = "#f5f6f7"

// BuildRoomBackTop is BuildRoomBack with the ceiling fade's source
// color set by the caller (the page reports its own chrome color) and
// the curtain wall on the day phase: posterized 2px bands lerp
// top→wallC across the wall's first 10 legacy px — the hem the
// transom beam picks up (y=10).
func BuildRoomBackTop(top color.RGBA) *image.NRGBA {
	return BuildRoomBackTod(top, TodDay)
}

// BuildRoomBackTod is BuildRoomBackTop with the curtain wall's view
// phase set by the caller — the client derives it from the real clock
// (art.js's todOf), day is the timeless default.
func BuildRoomBackTod(top color.RGBA, tod int) *image.NRGBA {
	img := NewCanvas(RoomW, RoomH)
	// the wall/floor rects below are legacy-unit (rect renders ×S), so
	// convert once up front (t_77: they used to feed WallH — a native
	// const — into rect, painting the wall at 2×WallH and burying the
	// desk rows inside it)
	vw, vh, wallH := RoomW/S, RoomH/S, WallH/S

	// --- wall ---
	rect(img, 0, 0, vw, wallH, wallC)
	// subtle horizontal banding
	for y := 0; y < wallH; y += 8 {
		rect(img, 0, y, vw, 1, wallShade)
	}
	// ceiling shadow: the wall emerges from the page chrome's color
	for i, c := range lerpBands(top, wallC, 5) {
		rect(img, 0, i*2, vw, 2, c)
	}
	// wainscot: light panels with vertical seams (office, not tavern)
	rect(img, 0, wallH-14, vw, 14, wainscot)
	for x := 6; x < vw; x += 12 {
		rect(img, x, wallH-14, 1, 12, C("#c1cbd3"))
	}
	rect(img, 0, wallH-2, vw, 2, baseboard)

	// --- floor: office carpet planks — horizontal seams lead, sparse
	// staggered verticals break them up, so the field reads as fabric
	// rather than a brick grid ---
	rect(img, 0, wallH, vw, vh-wallH, floorA)
	row := 0
	for y := wallH; y < vh; y += 7 {
		shade := floorA
		if row%2 == 1 {
			shade = floorB
		}
		rect(img, 0, y, vw, 7, shade)
		rect(img, 0, y, vw, 1, floorSeam)
		if row%2 == 1 { // verticals only on alternating rows
			for x := 14; x < vw; x += 28 {
				rect(img, x, y, 1, 7, floorSeam)
			}
		}
		// carpet flecks (deterministic)
		for i := 0; i < 9; i++ {
			px := (row*97 + i*53) % vw
			py := y + 2 + (row*31+i*17)%4
			if py*S < RoomH {
				img.Set(px*S, py*S, floorFleck) // rides the ×S grid with the floor rows
			}
		}
		row++
	}

	// --- the curtain wall: floor-to-ceiling glass, the city outside
	// on the real clock's phase ---
	drawCurtainWall(img, tod)

	// --- wall clock on the solid column between the panes ---
	ck := ClockRect()
	drawClock(img, ck.Min.X, ck.Min.Y)

	// --- 氛围墙挂（t_160，r_11）：主墙钟两侧的两件纯装饰 ---
	// 奖状框（钟左）：木框＋米底＋红绶带结＋两行「字」
	drawDiploma(img, 155, 22)
	// 团队合影（钟右柜左的空带，提至与挂钟齐平——下方让给收藏品货架）：
	// 黑框＋四枚彩点牛马＋笑眼
	drawTeamPhoto(img, 199, 18)

	// --- potted plant, bottom-left ---
	drawPlant(img, 14*S, WallH+4*S)
	// a second plant dressing the open east floor, pairing with the
	// corner one
	drawPlant(img, 302*S, WallH+2*S)

	// --- water dispenser, tucked into the far-left corner ---
	drawDispenser(img, 2*S, 52*S)

	// --- file cabinet against the curtain wall's center column, under
	// the clock and flush with the snack shelving — the glass stays
	// unobstructed floor-to-ceiling ---
	drawCase(img, 212*S, 30*S)

	// --- 收藏品货架（t_188 荣誉墙改版）：北墙中柱上的玻璃展示柜——
	// 像素窗柜面＋顶灯暖光＋两层货架，最近达成的成就奖杯摆进去（运行
	// 时由成就数据重绘），命中区与清单页签在前端 interact 层 ---
	drawShowcase(img)

	// ── 大厂开放工位：面对面对坐的 2×2 四人岛台，前后两排 ──
	pods := Pods()
	for i, pod := range pods {
		drawPodBack(img, pod, i*4)
	}
	// --- the pantry, the break corner above the meeting room ---
	drawPantry(img)

	// --- the meeting room, the south-east corner's glass box ---
	drawMeetingRoom(img)

	// --- the shared whiteboard, rolling at the bottom-left standup
	// corner (the curtain wall took the back wall) ---
	drawStandBoard(img)

	// --- the big printer flanking the whiteboard's east side (r_03) ---
	drawBigPrinter(img)

	return img
}

// BuildRoomFore renders the transparent foreground layer drawn over
// the actors: each island's near desk surfaces (occluding the
// camera-facing sitters' laps), desk props, armrest fronts and seam
// lamps — the occluders that turn "standing at a desk" into "sitting
// behind it". The away-facing rows' chairs are NOT here any more: they
// must cover their own sitter yet yield to anyone walking south of
// them, a depth no static layer can express — the board blits
// ChairSprite per seat, y-sorted among the actors (same for the
// meeting room's south row).
func BuildRoomFore() *image.NRGBA {
	img := NewCanvas(RoomW, RoomH)
	for i, pod := range Pods() {
		drawPodFore(img, pod, i*4)
	}
	return img
}

// ChairSprite renders the office's ONE task chair (drawTaskChair) as a
// standalone 18×18 legacy-px sprite for the canvas board: the board
// blits one per away-facing seat — the pods' near row and the meeting
// room's south row — y-sorted among the actors. The sprite's base sits
// 4 legacy px below its seat's spot, so it paints over its own sitter
// (feet at the spot) and under anyone south of it.
func ChairSprite() *image.NRGBA {
	img := NewCanvas(18*S, 18*S)
	drawTaskChair(img, 9, -26)
	return img
}

// ChairSpritePlacement returns the chair sprite's blit offset from an
// away-facing seat's spot (native px) and its native size — the same
// truth geom.json serves the board (server/art.go).
func ChairSpritePlacement() (dx, dy, w, h int) {
	return -9 * S, -14 * S, 18 * S, 18 * S
}

// rect fills a legacy-unit rectangle rendered at ×S (see S).
func rect(img *image.NRGBA, x, y, w, h int, c color.RGBA) {
	rectN(img, x*S, y*S, w*S, h*S, c)
}

// rectN fills a native-unit rectangle (new detail work draws here).
func rectN(img *image.NRGBA, x, y, w, h int, c color.RGBA) {
	FillRect(img, x, y, w, h, c)
}

func px(img *image.NRGBA, x, y int, c color.RGBA) { img.Set(x*S, y*S, c) }

// lerpBands builds n posterized steps from a to b (a and b included):
// the ceiling fade's discrete gradient, in the wall banding's spirit.
func lerpBands(a, b color.RGBA, n int) []color.RGBA {
	if n < 2 {
		return []color.RGBA{a}
	}
	out := make([]color.RGBA, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n-1)
		mix := func(x, y uint8) uint8 { return uint8(float64(x) + t*(float64(y)-float64(x))) }
		out[i] = color.RGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 255}
	}
	return out
}

// todPal is one phase's skyline palette: sky bands (zenith → horizon),
// the two tower layers, the window grid and its lit subset, the street
// below and the phase's celestial tells (stars, street lamps, spire
// beacons).
type todPal struct {
	sky     []color.RGBA
	far     color.RGBA // hazy far skyline
	near    color.RGBA // near tower glass
	nearDk  color.RGBA // towers' shadow edge + roofs
	win     color.RGBA // the unlit window grid
	winOn   color.RGBA // a lit window, warm
	winOn2  color.RGBA // the occasional cool one
	lit     int        // percent of windows lit (deterministic subset)
	ground  color.RGBA // the street band
	horizon color.RGBA // the horizon line
	lamp    color.RGBA // street lights
	lamps   bool
	stars   bool
	beacons bool // the spires' aircraft beacons
}

// todPals — day's blue-sky office hour, dusk's indigo-to-amber sunset,
// night's deep-blue grid of lit windows, dawn's pale rose wake-up.
var todPals = [4]todPal{
	{ // day
		sky: []color.RGBA{C("#6db9e0"), C("#84c8e8"), C("#9cd4ee"), C("#b5e0f3"), C("#cdeaf7")},
		far: C("#9fb6c8"), near: C("#6a8098"), nearDk: C("#4c5f75"),
		win: C("#7d94aa"), winOn: C("#ffe9a8"), winOn2: C("#b3d4ec"), lit: 0,
		ground: C("#93a7b6"), horizon: C("#b9cade"),
	},
	{ // dusk
		sky: []color.RGBA{C("#3b4877"), C("#5c5688"), C("#8a5d84"), C("#bb6a72"), C("#e2925f"), C("#f6ba6c")},
		far: C("#6e6086"), near: C("#413e5e"), nearDk: C("#322f4c"),
		win: C("#4d4968"), winOn: C("#ffd27a"), winOn2: C("#ffb46a"), lit: 32,
		ground: C("#4b4358"), horizon: C("#d99a72"),
		lamp: C("#ffc878"), lamps: true, beacons: true,
	},
	{ // night
		sky: []color.RGBA{C("#0f1730"), C("#151f3c"), C("#1c2947"), C("#233254"), C("#2b3c61")},
		far: C("#242e4e"), near: C("#182036"), nearDk: C("#10162a"),
		win: C("#222b46"), winOn: C("#ffd97a"), winOn2: C("#9fc2e8"), lit: 55,
		ground: C("#0d1322"), horizon: C("#33415f"),
		lamp: C("#ffcf7a"), lamps: true, stars: true, beacons: true,
	},
	{ // dawn
		sky: []color.RGBA{C("#47598a"), C("#6f7fa6"), C("#9c8cab"), C("#cfa0a6"), C("#eec4a9"), C("#f7ddb9")},
		far: C("#7f89a4"), near: C("#4f5a76"), nearDk: C("#3c4660"),
		win: C("#5c6784"), winOn: C("#ffe3a3"), winOn2: C("#cfe0f2"), lit: 14,
		ground: C("#59607a"), horizon: C("#e8c5a8"),
	},
}

// tower is one CBD tower in the curtain wall's view: dark glass from
// top down to the street, an optional stepped crown (setback narrows
// the top section on both sides), an optional antenna spire, and the
// deterministic window grid that lights up after dark.
type tower struct {
	x, w, top int
	setback   int
	spire     bool
}

// towers lays both panes' skyline: the west pane's stepped slab and
// antenna mid-rise with sky gaps, the east pane's hero spire and twin
// towers — one dense CBD read through two windows of the same room.
var towers = []tower{
	// west pane (glass 24..170)
	{x: 28, w: 24, top: 22, setback: 7},
	{x: 58, w: 16, top: 30, spire: true},
	{x: 80, w: 14, top: 36},
	{x: 102, w: 26, top: 24},
	{x: 134, w: 18, top: 32, setback: 4},
	{x: 156, w: 12, top: 40},
	// east pane (glass 268..354)
	{x: 270, w: 18, top: 26, setback: 5},
	{x: 292, w: 26, top: 21, spire: true},
	{x: 322, w: 13, top: 25},
	{x: 337, w: 13, top: 25},
}

// drawCurtainWall paints the whole curtain wall: both panes' city view
// in the phase's palette, then the structure over it — transom, base
// band, jamb posts and the mullion grid. The column between the panes
// stays solid wall (the clock's and the snack shelves' ground).
func drawCurtainWall(img *image.NRGBA, tod int) {
	p := todPals[tod]
	for _, span := range [][2]int{{paneL0, paneL1}, {paneR0, paneR1}} {
		drawPaneView(img, span[0], span[1], p, span[0] == paneL0, tod)
		drawPaneFrame(img, span[0], span[1])
	}
}

// drawPaneView paints one pane's glass: sky bands, the phase's
// celestial tells, the far hazy skyline, the street band and the near
// CBD towers. west places the day sun and the dusk sunset (the sun
// sets in the west) and picks the east pane for the moon and the dawn
// sun.
func drawPaneView(img *image.NRGBA, x0, x1 int, p todPal, west bool, tod int) {
	gw := x1 - x0 - 4 // glass width, inside the 2px jambs
	// sky: posterized bands, zenith → horizon
	for i, c := range p.sky {
		y0 := paneTop + (paneHorizon-paneTop)*i/len(p.sky)
		y1 := paneTop + (paneHorizon-paneTop)*(i+1)/len(p.sky)
		rect(img, x0+2, y0, gw, y1-y0, c)
	}
	switch tod {
	case TodDay:
		if west { // the high afternoon sun
			drawSun(img, 128, 17, C("#f7d154"), C("#ffe9a8"))
		}
		// two little clouds
		rect(img, x0+18, 21, 5, 1, cloudC)
		rect(img, x0+19, 20, 3, 1, cloudC)
		rect(img, x0+52, 28, 4, 1, cloudC)
		rect(img, x0+53, 27, 2, 1, cloudC)
	case TodDusk:
		if west { // the setting sun, half-swallowed by the street
			drawSun(img, 94, 47, C("#ff9d54"), C("#ffc87a"))
		}
	case TodNight:
		for i := 0; i < 14; i++ { // stars
			px(img, x0+4+(i*53)%(gw-4), 15+(i*29)%16, C("#dfe6f2"))
		}
		if !west { // the moon rides the east pane
			rect(img, 340, 17, 4, 4, C("#e8ecf2"))
			px(img, 342, 18, C("#c9d2dd"))
			px(img, 341, 21, C("#cfd8e2"))
		}
	case TodDawn:
		if !west { // a pale sun climbing out of the east towers
			drawSun(img, 317, 44, C("#f2c98e"), C("#ffe3b0"))
		}
	}
	// the far skyline: hazy step towers riding the horizon
	for bx := x0 + 4; bx < x1-10; bx += 8 {
		h := 9 + ((bx+x0)/8*37)%15
		rect(img, bx, paneHorizon-h, 7, h, p.far)
	}
	// the street band clips the far layer's feet (and the setting sun)
	rect(img, x0+2, paneHorizon, gw, paneBot-paneHorizon, p.ground)
	rect(img, x0+2, paneHorizon, gw, 1, p.horizon)
	// the CBD's own towers, near and dark-glassed
	for _, t := range towers {
		if t.x < x0+2 || t.x+t.w > x1-2 {
			continue
		}
		drawTower(img, t, p)
	}
	// street lights, sparse at dusk, a proper run at night
	if p.lamps {
		step := 13
		if tod == TodDusk {
			step = 19
		}
		for lx := x0 + 8; lx < x1-6; lx += step {
			rect(img, lx, 55, 2, 1, p.lamp)
		}
	}
}

// drawTower paints one CBD tower: body with a shadowed east edge and
// roof band, optional stepped crown and antenna spire with its beacon,
// and the window grid — every window a dark glass tick, a
// deterministic warm-lit subset (the odd cool one) after dark.
func drawTower(img *image.NRGBA, t tower, p todPal) {
	base := paneGroundY
	if t.setback > 0 {
		shoulder := t.top + 8
		rect(img, t.x, shoulder, t.w, base-shoulder, p.near)
		rect(img, t.x+t.w-2, shoulder, 2, base-shoulder, p.nearDk)
		rect(img, t.x+t.setback, t.top, t.w-2*t.setback, shoulder-t.top, p.near)
		rect(img, t.x+t.w-t.setback-2, t.top, 2, shoulder-t.top, p.nearDk)
		rect(img, t.x+t.setback, t.top, t.w-2*t.setback, 2, p.nearDk)
		px(img, t.x+t.w/2, t.top+3, p.win) // the crown's one lit row
	} else {
		rect(img, t.x, t.top, t.w, base-t.top, p.near)
		rect(img, t.x+t.w-2, t.top, 2, base-t.top, p.nearDk)
		rect(img, t.x, t.top, t.w, 2, p.nearDk)
	}
	if t.spire {
		mx := t.x + t.w/2 - 1
		rect(img, mx, t.top-6, 2, 6, p.nearDk)
		if p.beacons {
			px(img, mx, t.top-7, C("#ff5a4e"))
		}
	}
	lo := t.top + 2
	if t.setback > 0 {
		lo = t.top + 10
	}
	for wy := lo; wy+2 <= base-1; wy += 3 {
		for wx := t.x + 2; wx+1 < t.x+t.w-1; wx += 3 {
			c := p.win
			if h := (wx*31 + wy*17) % 100; h < p.lit {
				c = p.winOn
				if h%7 == 0 {
					c = p.winOn2
				}
			}
			rect(img, wx, wy, 1, 2, c)
		}
	}
}

// drawSun paints a small pixel sun disc (5×4) with a lighter core —
// day's rides high, dusk's and dawn's sit low where the street band
// swallows their feet.
func drawSun(img *image.NRGBA, x, y int, c, core color.RGBA) {
	rect(img, x+1, y, 3, 1, c)
	rect(img, x, y+1, 5, 2, c)
	rect(img, x+1, y+3, 3, 1, c)
	rect(img, x+1, y+1, 3, 1, core)
}

// the curtain wall's own palette — one charcoal frame family for
// transom, jambs, mullions and base.
var (
	mullC  = C("#2b3038") // anodized charcoal
	mullHi = C("#454b54") // the lit edge every member carries
	mullDk = C("#22262d") // the base band's heavier shade
)

// drawPaneFrame paints one pane's structure over its view: the
// transom beam at the ceiling fade's hem, the base band down to the
// floor, the mullion grid and the jamb posts over everything.
func drawPaneFrame(img *image.NRGBA, x0, x1 int) {
	rect(img, x0, 10, x1-x0, 4, mullC) // transom
	rect(img, x0, 10, x1-x0, 1, mullHi)
	rect(img, x0, paneBot, x1-x0, 64-paneBot, mullDk) // base band
	rect(img, x0, paneBot, x1-x0, 1, mullHi)
	for mx := x0 + paneBayW; mx < x1-2; mx += paneBayW {
		rect(img, mx, paneTop, 2, paneBot-paneTop, mullC)
		rect(img, mx, paneTop, 1, paneBot-paneTop, mullHi)
	}
	for _, jx := range []int{x0, x1 - 2} { // jambs over everything
		rect(img, jx, 10, 2, 54, mullC)
		rect(img, jx, 10, 1, 54, mullHi)
	}
}

func drawClock(img *image.NRGBA, x, y int) {
	x, y = x/S, y/S // native rect in, legacy units inside (rect scales ×S)
	rows := []string{
		"..KKKKKK..",
		".KWWWWWWK.",
		"KWWWKWWWWK",
		"KWWWWKWWWK",
		"KWWWWWKWWK",
		"KWWWWWWWWK",
		"KWWWKWWWWK",
		"KWWWKKWWWK",
		".KWWWWWWK.",
		"..KKKKKK..",
	}
	Blit(img, Render(rows, Palette{
		'K': metalC, 'W': clockFace,
	}), x*S, y*S)
}

// drawWhiteboard paints the whiteboard face: aluminum frame, white
// face, marker content like a stand-up board (blue title, gray
// bullets, one red blocker line, a green done box) and a tray with
// markers and an eraser — the same face the wall used to wear, now
// carried by the floor stand (drawStandBoard).
func drawWhiteboard(img *image.NRGBA, x, y int) {
	x, y = x/S, y/S // native rect in, legacy units inside (rect scales ×S)
	bw, bh := WhiteboardW/S, WhiteboardH/S
	// aluminum frame + top bevel
	rect(img, x, y, bw, bh, boardFrameC)
	rect(img, x, y, bw, 1, C("#d9dfe5"))
	// white face with an inner top shade
	rect(img, x+2, y+2, bw-4, bh-5, boardFaceC)
	rect(img, x+2, y+2, bw-4, 1, C("#e7ebef"))
	// marker content: title bar, bullet lines, one red blocker
	rect(img, x+5, y+5, 12, 2, markerBlue)
	for i, wl := range []int{20, 16, 22} {
		rect(img, x+5, y+10+i*3, wl, 1, markerGray)
	}
	rect(img, x+5, y+19, 8, 1, markerRed)
	// the done box: green-tinted panel with a check line
	rect(img, x+30, y+9, 9, 8, C("#eaf4ea"))
	rect(img, x+30, y+9, 9, 1, C("#9ccf9b"))
	rect(img, x+30, y+16, 9, 1, C("#9ccf9b"))
	rect(img, x+30, y+9, 1, 8, C("#9ccf9b"))
	rect(img, x+38, y+9, 1, 8, C("#9ccf9b"))
	rect(img, x+32, y+12, 5, 1, markerGreen)
	// tray sticking out of the bottom frame, markers + eraser on it
	rect(img, x+4, y+bh-2, bw-8, 2, C("#aeb8c1"))
	px(img, x+8, y+bh-3, markerBlue)
	px(img, x+11, y+bh-3, markerRed)
	px(img, x+14, y+bh-3, C("#1f2329"))
	rect(img, x+30, y+bh-3, 6, 2, C("#b98f6a"))
}

func drawPlant(img *image.NRGBA, x, y int) {
	x, y = x/S, y/S // native rect in, legacy units inside (rect scales ×S)
	rows := []string{
		"..gG..Gg..",
		".gGGGgGGg.",
		"gGGgGgGGGg",
		"gGGGGGGgGg",
		".gGGgGGGg.",
		"..GgGgGg..",
		"...gGGg...",
		"....Gg....",
		"...CCCCCC..",
		"..CCCCCCC..",
		"..cCCCCc...",
		"..cCCCCc...",
		"...cCCc....",
		"...cCCc....",
	}
	Blit(img, Render(rows, Palette{
		'G': leafC, 'g': leafD, 'C': terraC, 'c': terraD,
	}), x*S, y*S)
}

// drawDispenser paints the floor-standing water dispenser: blue
// bottle on a white body, cold/hot taps, recessed cup panel, dark
// base — the office corner the plant doesn't own.
func drawDispenser(img *image.NRGBA, x, y int) {
	x, y = x/S, y/S // native rect in, legacy units inside (rect scales ×S)
	rows := []string{
		"...eeee...",
		"..eWWWWe..",
		"..eWlWWe..",
		"..eWWWWe..",
		"..eWWWWe..",
		"..eWWWWe..",
		".eeeeeeee.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wcwwwwhw.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wddddddw.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wddddddw.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wwwwwwww.",
		".wwwwwwww.",
		".gggggggg.",
		"gggggggggg",
		"gggggggggg",
		"gggggggggg",
	}
	Blit(img, Render(rows, Palette{
		'e': C("#9fd6ee"), 'W': C("#bfe3f2"), 'l': cloudC,
		'w': dispenserC, 'd': C("#d9dfe5"),
		'c': markerBlue, 'h': markerRed, 'g': C("#9aa4ad"),
	}), x*S, y*S)
}

// drawCase paints the low file cabinet against the wall: light body,
// two shelves of colored binders, a drawer row and a tiny plant on
// top — storage every office has and no classroom does.
func drawCase(img *image.NRGBA, x, y int) {
	x, y = x/S, y/S // native rect in, legacy units inside (rect scales ×S)
	w, h := 26, 34
	rect(img, x, y, w, h, caseBodyC)
	// outline
	rect(img, x, y, w, 1, caseLineC)
	rect(img, x, y+h-1, w, 1, caseLineC)
	rect(img, x, y, 1, h, caseLineC)
	rect(img, x+w-1, y, 1, h, caseLineC)
	// two binder shelves (row order shuffled so they don't read copy-paste)
	shelves := [][6]color.RGBA{
		{markerBlue, C("#7fb0d8"), markerGreen, C("#ff8800"), markerRed, C("#8f959e")},
		{C("#8f959e"), markerRed, markerBlue, C("#ff8800"), C("#7fb0d8"), markerGreen},
	}
	for i, cols := range shelves {
		sy := y + 2 + i*11
		for j, c := range cols {
			rect(img, x+2+j*4, sy, 3, 9, c)
		}
		rect(img, x+1, sy+10, w-2, 1, caseLineC) // shelf board
	}
	// drawer row
	rect(img, x+9, y+27, 8, 2, caseLineC) // handle
	rect(img, x+1, y+30, w-2, 1, caseLineC)
	// tiny plant on top
	plantRows := []string{
		".gGGg.",
		"gGGGGg",
		".gGGg.",
		"..GG..",
		".tttt.",
		".tttt.",
	}
	Blit(img, Render(plantRows, Palette{
		'G': leafC, 'g': leafD, 't': terraC,
	}), (x+10)*S, (y-6)*S)
}

// drawPodBack paints an island's behind-the-actor layer: the
// camera-facing row's ergonomic chair rears (headrest wing + full mesh
// backrest), the away-facing row's desk surfaces with their paper
// props, and the shared bench spine (modesty panel + legs) — everything
// a seated body covers.
func drawPodBack(img *image.NRGBA, pod Pod, base int) {
	x, y := pod.X/S, pod.Y/S // native rect in, legacy units inside
	stride := (SeatW + podSeam) / S
	for c := 0; c < seatsPerRow; c++ {
		sx, cx := x+c*stride, x+c*stride+18
		// far (camera-facing) chair: the SAME task chair as everywhere
		// else in the office (drawTaskChair), seated line at y+12 —
		// drawn behind the sitter; the desk (fore) hides the seat edge
		// and the bench spine below hides column and base
		drawTaskChair(img, cx, y-28)
		// near (away-facing) desk: surface with a paper stack + mug
		rect(img, sx, y+20, 36, 3, deskTopC)
		rect(img, sx, y+23, 36, 1, deskEdgeC)
		rect(img, sx+24, y+20, 6, 1, folderC(base+2+c))
		rect(img, sx+24, y+21, 6, 1, paperC)
		rect(img, sx+24, y+22, 6, 1, paperShade)
		if (base+2+c)%4 == 3 {
			rect(img, sx+31, y+20, 3, 3, mugBlue)
		} else {
			rect(img, sx+31, y+20, 3, 3, mugRed)
		}
	}
	// the bench spine: shared modesty panel between the facing desks,
	// end legs and a low crossbar
	rect(img, x+2, y+12, 70, 8, pedestalC)
	rect(img, x+2, y+12, 70, 1, caseLineC)
	rect(img, x+2, y+19, 2, 16, legC)
	rect(img, x+70, y+19, 2, 16, legC)
	rect(img, x+4, y+33, 66, 1, legC)
}

// folderC rotates the paper-stack folder through the brand accents so
// a row never reads copy-pasted.
func folderC(i int) color.RGBA {
	return [3]color.RGBA{markerBlue, C("#ff8800"), markerGreen}[i%3]
}

// drawPodFore paints an island's over-the-actor layer, in painter's
// order: the camera-facing row's desk (surface, props, east lamp,
// armrest fronts — occluding the sitter's lap) and its west lamp
// standing on the shared seam. The near row's chair is not here: it
// blits as ChairSprite, y-sorted among the actors by the canvas board.
func drawPodFore(img *image.NRGBA, pod Pod, base int) {
	x, y := pod.X/S, pod.Y/S // native rect in, legacy units inside
	stride := (SeatW + podSeam) / S
	for c := 0; c < seatsPerRow; c++ {
		sx, cx := x+c*stride, x+c*stride+18
		i := base + c
		// far desk top: light oak with a darker front lip — occludes
		// the far sitter's lap
		rect(img, sx, y+8, 36, 3, deskTopC)
		rect(img, sx, y+11, 36, 1, deskEdgeC)
		// armrest fronts curling over the desk edge
		rect(img, cx-9, y+5, 2, 4, chairDkC)
		rect(img, cx+7, y+5, 2, 4, chairDkC)
		rect(img, cx-9, y+5, 2, 1, chairHiC)
		rect(img, cx+7, y+5, 2, 1, chairHiC)
		// east lamp: base on the tabletop, slim arm, overhanging shade
		rect(img, sx+29, y+4, 1, 4, legC)
		rect(img, sx+28, y+8, 3, 1, legC)
		rect(img, sx+26, y, 8, 3, lampOffC)
		rect(img, sx+26, y+3, 8, 1, lampOffDkC)
		// paper stack lying toward the desk's front edge, over the
		// sitter's lap; folder rotates through the brand accents
		rect(img, sx+14, y+9, 6, 1, folderC(i))
		rect(img, sx+14, y+10, 6, 1, paperC)
		rect(img, sx+14, y+11, 6, 1, paperShade)
		// center-east: coffee mug — every third desk wears the little
		// plant instead (the west corner belongs to the far row's lamp)
		if i%3 == 1 {
			rect(img, sx+21, y+8, 4, 2, leafC)
			px(img, sx+23, y+8, leafD)
			rect(img, sx+22, y+10, 3, 2, terraC)
		} else {
			rect(img, sx+21, y+9, 3, 3, mugRed)
			px(img, sx+24, y+10, C("#efe9dc")) // handle
		}
		// west lamp rising from the near desk's far edge, in front of
		// the bench spine
		rect(img, sx+2, y+15, 8, 3, lampOffC)
		rect(img, sx+2, y+18, 8, 1, lampOffDkC)
		rect(img, sx+5, y+18, 1, 2, legC)
		rect(img, sx+4, y+19, 4, 1, legC)
	}
}

// drawStandBoard paints the floor-standing rolling whiteboard at the
// office's bottom-left standup corner: the same 44×26 board face the
// back wall used to wear (drawWhiteboard, markers and all), now
// riding two side posts and a caster base — the wall went glass, the
// board went downstairs (黑板什么的放办公室下方).
func drawStandBoard(img *image.NRGBA) {
	x, y := WhiteboardRect().Min.X/S, WhiteboardRect().Min.Y/S
	drawWhiteboard(img, x*S, y*S)
	// side posts down to the base
	rect(img, x+6, y+26, 2, 10, legC)
	rect(img, x+36, y+26, 2, 10, legC)
	// the base rail on two caster wheels
	rect(img, x-4, y+36, 52, 3, legC)
	rect(img, x-4, y+36, 52, 1, metalC)
	rect(img, x-3, y+39, 4, 3, metalC)
	rect(img, x-3, y+42, 4, 1, mullDk)
	rect(img, x+41, y+39, 4, 3, metalC)
	rect(img, x+41, y+42, 4, 1, mullDk)
}

// drawBigPrinter paints the 大型打印机 (r_03): a three-deck floor
// MFP east of the rolling whiteboard. Top deck: brushed-steel scanner
// lid with the output tray in front of it, one white sheet always
// lying in the tray (the board slides the printed one out over it).
// Middle deck: the slanted control panel — dark little screen, status
// LED (dark in art; the board lights it brand-blue while printing) and
// two keys. Bottom deck: the paper cabinet, two slab doors and a kick.
// Legacy-unit rects inside (anchor (103,226) legacy = native (206,452),
// the body's top-left; every rect below scales ×S into native).
func drawBigPrinter(img *image.NRGBA) {
	x, y := 103, 226 // legacy anchor；×S 后 native (206,452)——白板东侧同排、半高、底对齐
	// 尺寸：宽 20 legacy（40 native），高 21 legacy（42 native）——半高机型，
	// 底边 y226+21=247 legacy=494 native 对齐白板腿
	// ── 顶段：扫描盖（拉丝钢）＋出纸托盘 ──
	rect(img, x, y, 20, 2, C("#aab4bd"))     // 扫描盖顶面
	rect(img, x, y, 20, 1, C("#c3ccd3"))     // 盖顶高光
	rect(img, x+1, y+2, 18, 1, C("#8f99a3")) // 盖沿阴影
	// 出纸托盘：斜面托盘纸上常驻一张白纸（画进图层；打印时画布在这上面滑出新的）
	rect(img, x+3, y+3, 14, 1, C("#98a2ac")) // 托盘斜面
	rect(img, x+2, y+4, 16, 1, C("#aab4bd")) // 托盘前沿
	rect(img, x+4, y+2, 11, 1, paperC)       // 常驻白纸
	// ── 中段：操作面板带（斜面屏＋按键＋LED）──
	rect(img, x, y+5, 20, 2, C("#dfe4ea"))   // 面板带底（斜面）
	rect(img, x, y+5, 20, 1, C("#eef1f6"))   // 面板顶光
	rect(img, x+1, y+6, 9, 1, C("#23282f"))  // 屏（暗）
	rect(img, x+2, y+6, 7, 1, C("#3d454f"))  // 屏反光
	rect(img, x+12, y+6, 2, 1, C("#3b4048")) // 按键
	rect(img, x+15, y+6, 2, 1, C("#3b4048"))
	rect(img, x+18, y+6, 1, 1, C("#2c3440")) // LED 暗座（画布点亮）
	// ── 下段：纸柜（半高机身的主段，双门＋出纸盒口）──
	rect(img, x, y+7, 20, 12, C("#e8ebee"))   // 柜身
	rect(img, x, y+7, 20, 1, C("#d5dae0"))    // 柜顶缝
	rect(img, x, y+7, 1, 12, C("#d5dae0"))    // 左框
	rect(img, x+19, y+7, 1, 12, C("#d5dae0")) // 右框
	rect(img, x+9, y+8, 1, 10, C("#d0d6db"))  // 门中缝
	rect(img, x+3, y+10, 1, 3, metalC)        // 左门把手
	rect(img, x+6, y+10, 1, 3, metalC)        // 右门把手
	// 正面出纸盒口（复合机标志）
	rect(img, x+12, y+9, 6, 3, C("#dfe4ea"))  // 纸盒
	rect(img, x+13, y+10, 4, 1, C("#c4ccd4")) // 盒口缝
	// 踢脚＋底座（底边 y+21=247 legacy=494 native，对齐白板腿）
	rect(img, x+1, y+17, 18, 2, C("#9aa4ad")) // 踢脚
	rect(img, x+1, y+19, 18, 2, C("#8f99a3")) // 底座
}

// drawPantry paints the 茶水间: one long L-shaped counter filling the
// east half of the floor above the meeting room — the north arm runs
// the wall's foot nearly wall-to-wall, the east arm rides the scene's
// east edge down. Quartz top, matte flat-panel cabinets with the
// sink bay as a three-drawer stack, an integrated steel fridge
// (magnets + note) closing the north arm's east end; on the worktop a
// coffee machine, gooseneck kettle, inset sink with faucet (洗杯子),
// a drying mat with mugs, a cup dispenser and a red/blue jar pair;
// open snack shelving hangs over the west end (零食架) while the east
// arm's body is an open storage unit of oak shelves and colorful
// boxes; two bar stools pull up to the north arm's south face.
// Legacy-unit rects, ×2 into the Pantry* accessors' native geometry
// (min/max form) — art and hit geometry stay one truth.
func drawPantry(img *image.NRGBA) {
	// ── 北臂：石英台面＋哑光平板柜，横贯墙脚 ──
	rect(img, 236, 84, 148, 4, quartzC)      // 台面（西端出檐 1）
	rect(img, 236, 88, 148, 1, quartzEdgeC)  // 台面阴影线
	rect(img, 237, 89, 127, 16, pantryCabC)  // 哑光柜身（冰箱东侧另算）
	rect(img, 237, 89, 127, 1, C("#d5dae0")) // 柜顶缝
	rect(img, 237, 89, 1, 16, C("#d5dae0"))  // 左边框
	rect(img, 363, 89, 1, 16, C("#d5dae0"))  // 右边框
	// 平板柜门：西端第一格改三层抽屉（mock 的横条拉手节奏），余下
	// 4 扇均分门——边缝＋右下角明装短把手，踢脚内收 2
	for i := 0; i < 5; i++ {
		dx := 237 + i*25
		if i == 0 { // 水槽柜：三层抽屉——横缝分隔＋居中短横条拉手
			for d := 0; d < 3; d++ {
				dy := 90 + d*5
				rect(img, 238, dy, 22, 4, pantryCabC)   // 抽屉脸
				rect(img, 238, dy, 22, 1, C("#d5dae0")) // 抽屉顶棱
				rect(img, 247, dy+2, 4, 1, metalC)      // 横条拉手
			}
			rect(img, 238, 94, 22, 1, pantryLineC) // 抽屉间缝
			rect(img, 238, 99, 22, 1, pantryLineC)
			continue
		}
		rect(img, dx, 90, 24, 14, pantryCabC) // 门板
		rect(img, dx, 90, 1, 14, pantryLineC) // 门间缝
		rect(img, dx+20, 100, 3, 1, metalC)   // 明装把手
	}
	rect(img, 239, 104, 123, 1, C("#d5dae0")) // 柜底线
	rect(img, 239, 105, 123, 2, pantryKickC)  // 踢脚（内收 2）

	// ── 嵌入式冰箱：北臂东端，与柜身同深一体嵌入（钢门双开＋竖把手）──
	rect(img, 364, 84, 20, 4, quartzC)
	rect(img, 364, 88, 20, 1, quartzEdgeC)
	rect(img, 364, 89, 20, 16, pantrySteelC) // 钢门面
	rect(img, 364, 89, 20, 1, C("#b9c1c9"))  // 顶缝
	rect(img, 364, 89, 1, 16, C("#b9c1c9"))  // 左框
	rect(img, 383, 89, 1, 16, C("#b9c1c9"))  // 右框
	rect(img, 373, 89, 1, 16, C("#b9c1c9"))  // 对开门中缝
	rect(img, 367, 92, 1, 9, metalC)         // 左门竖把手（贴左框）
	rect(img, 375, 92, 1, 9, metalC)         // 右门竖把手（贴中缝右）
	rect(img, 366, 102, 16, 1, C("#b9c1c9")) // 底缝
	px(img, 381, 91, screenLedC)             // 门上小红灯
	// 门面生活气：四枚圆磁贴＋一张小便签（mock 的冰箱贴语言——冷钢门
	// 上唯一的人气处，红黄蓝绿各占一枚、便签压在左门下角）
	px(img, 369, 93, markerRed)
	px(img, 371, 95, C("#f2c14e"))
	px(img, 378, 94, markerBlue)
	px(img, 380, 97, markerGreen)
	rect(img, 369, 98, 3, 2, paperC) // 小便签

	// ── 东臂：贴东缘南下——台面之下改开放搁板（mock v2 的彩色收纳
	// 架）：侧立柱＋浅木层板，四格里码着彩盒、纸袋与罐子——一排闭门
	// 高柜读作一段灰墙，开放格子给冷色茶水间一点零食角的烟火气 ──
	rect(img, 364, 82, 20, 4, quartzC) // 转角台面
	rect(img, 381, 84, 3, 70, quartzC) // 东臂台面竖带（x 762..768 贴到边）
	rect(img, 381, 154, 3, 1, quartzEdgeC)
	rect(img, 367, 87, 13, 68, C("#d9dde3")) // 搁板背板
	rect(img, 366, 86, 1, 70, pantryCabC)    // 西立柱
	rect(img, 380, 86, 1, 70, pantryCabC)    // 东立柱
	rect(img, 366, 86, 15, 1, C("#d5dae0"))  // 顶缝
	rect(img, 366, 86, 1, 70, C("#d5dae0"))  // 西柱外棱
	rect(img, 380, 86, 1, 70, C("#d5dae0"))  // 东柱外棱
	for i := 0; i < 4; i++ {                 // 浅木层板×4：面板＋下缘影（工位橡木同款暖色）
		sy := 101 + i*17
		rect(img, 367, sy, 13, 1, deskTopC)
		rect(img, 367, sy+1, 13, 1, deskEdgeC)
	}
	// 第一格：三只彩盒（品牌红/蓝＋芥末黄，盒盖一像素深一档）
	rect(img, 368, 95, 3, 6, markerRed)
	px(img, 368, 94, C("#c93a35"))
	rect(img, 372, 94, 4, 7, markerBlue)
	px(img, 372, 93, C("#2c5fd0"))
	rect(img, 377, 96, 3, 5, C("#f2c14e"))
	// 第二格：白纸袋×2＋红袋（袋口折角一像素）
	rect(img, 368, 112, 3, 6, paperC)
	px(img, 369, 111, C("#c9cfd6"))
	rect(img, 372, 113, 3, 5, paperC)
	px(img, 373, 112, C("#c9cfd6"))
	rect(img, 376, 111, 3, 7, C("#d03a35"))
	px(img, 377, 110, C("#b02c28"))
	// 第三格：深色罐×2＋绿瓶＋琥珀罐（高矮错落）
	rect(img, 368, 130, 2, 5, C("#555a63"))
	rect(img, 371, 130, 2, 5, C("#555a63"))
	rect(img, 374, 129, 2, 6, markerGreen)
	px(img, 374, 128, C("#2a9e1d"))
	rect(img, 377, 131, 2, 4, C("#d98e32"))
	// 第四格（底）：白储物箱＋蓝水瓶×2（箱盖线＋箱扣）
	rect(img, 368, 146, 6, 6, paperC)
	rect(img, 368, 146, 6, 1, C("#d9dfe4"))
	rect(img, 369, 149, 4, 1, C("#c9cfd6"))
	rect(img, 375, 147, 2, 5, C("#9fd6ee"))
	px(img, 375, 146, markerBlue)
	rect(img, 377, 148, 2, 4, C("#9fd6ee"))
	rect(img, 366, 155, 15, 1, C("#d5dae0")) // 底收边

	// ── 台面设备（自西向东）：水槽、纸巾卷、沥水马克杯、杯托机、
	// 咖啡机、水壶、微波炉、果盘、碟堆 ──
	// 洗杯水槽：内嵌凹槽＋鹅颈龙头（洗杯子的地方）
	rect(img, 240, 85, 14, 3, C("#dfe4ea")) // 水槽凹面
	rect(img, 241, 86, 12, 1, C("#c4ccd4")) // 槽底阴影
	px(img, 252, 82, metalC)                // 龙头基座
	rect(img, 252, 80, 1, 2, metalC)        // 鹅颈立柱
	rect(img, 250, 80, 2, 1, metalC)        // 鹅颈弯头
	// 纸巾卷：白卷＋抽纸口
	rect(img, 256, 81, 5, 3, C("#f7f9fa"))
	rect(img, 257, 82, 3, 1, C("#d9dfe4"))
	// 沥水垫上倒扣的两只马克杯
	rect(img, 263, 84, 3, 2, mugBlue)
	rect(img, 267, 84, 3, 2, mugRed)
	// 杯托机：一摞纸杯挂在架里
	rect(img, 272, 82, 5, 3, C("#f7f9fa"))
	rect(img, 273, 83, 3, 1, C("#dfe4ea"))
	// 咖啡机（台面主角）：机身＋豆仓视窗＋就绪灯＋出水口＋接杯
	rect(img, 280, 77, 11, 7, metalC)
	rect(img, 281, 78, 9, 3, C("#23282f"))
	px(img, 289, 80, markerGreen)
	rect(img, 283, 82, 5, 1, C("#23282f"))
	rect(img, 284, 83, 3, 1, mugRed) // 出水口下的杯子
	// 水壶：白壶身＋壶盖＋壶嘴＋加热底座
	rect(img, 294, 79, 6, 4, C("#f0f3f6"))
	rect(img, 295, 78, 4, 1, C("#d9dfe4"))
	rect(img, 300, 80, 1, 2, C("#d9dfe4"))
	rect(img, 293, 83, 8, 1, legC)
	// 糖罐一对：红蓝带盖（mock 台面上的彩色罐——灰台面里最近的色
	// 点；5px 高才读得出，水壶的白身正好当对比底）
	rect(img, 302, 79, 2, 5, markerRed)
	rect(img, 302, 78, 2, 1, C("#b23f33"))
	px(img, 302, 80, C("#e88a7f")) // 罐身高光
	rect(img, 304, 79, 2, 5, mugBlue)
	rect(img, 304, 78, 2, 1, C("#436f9e"))
	px(img, 304, 80, C("#93b4d8"))
	// 微波炉：钢身＋深色门视窗＋门把手＋加热灯
	rect(img, 306, 76, 16, 8, C("#c8cfd6"))
	rect(img, 307, 77, 9, 6, C("#23282f"))  // 门视窗
	rect(img, 308, 78, 7, 2, C("#3d454f"))  // 视窗反光
	rect(img, 317, 77, 1, 6, metalC)        // 门把手
	rect(img, 306, 75, 16, 1, C("#aeb6be")) // 顶盖线
	px(img, 320, 83, markerGreen)           // 加热灯
	// 果盘：浅盘＋三只橙子
	rect(img, 326, 81, 12, 3, C("#d9d2bd"))
	px(img, 328, 80, C("#f7a331"))
	px(img, 331, 79, C("#f7a331"))
	px(img, 334, 80, C("#e08a1f"))
	// 碟堆：三只白碟侧影
	rect(img, 342, 82, 8, 1, C("#fbfbf8"))
	rect(img, 343, 83, 8, 1, C("#e7ebef"))
	rect(img, 342, 84, 8, 1, C("#fbfbf8"))

	// ── 衣帽钩（t_160 氛围点缀）：零食架与菜单板之间的墙面——黄铜
	// 挂条＋三钩，挂着一条闲置的围巾与一顶帽子（办公室有人怕冷）──
	rect(img, 265, 66, 10, 2, C("#c9a84c")) // brass rail
	px(img, 266, 68, metalC)                // hook ×3
	px(img, 270, 68, metalC)
	px(img, 274, 68, metalC)
	// 挂物一：围巾（第一个钩上垂两条）
	rect(img, 265, 69, 2, 7, C("#b03030"))
	rect(img, 268, 69, 2, 5, C("#8a3a3a"))
	// 挂物二：帽子（第二个钩上一顶软帽的侧影）
	rect(img, 270, 69, 5, 3, C("#7a5c44"))
	rect(img, 269, 71, 7, 1, C("#5c4433")) // 檐

	// ── 菜单板：零食架与冰箱之间墙上的小黑板（今日咖啡价目）──
	rect(img, 276, 60, 24, 18, C("#7a5c44")) // 木框
	rect(img, 278, 62, 20, 14, C("#23282f")) // 黑板面
	rect(img, 280, 64, 8, 2, C("#f2c14e"))   // 标题（粉笔黄）
	rect(img, 280, 68, 12, 1, C("#c9d2dc"))  // 品名行
	px(img, 295, 68, C("#f2c14e"))           // 价点
	rect(img, 280, 71, 10, 1, C("#c9d2dc"))
	px(img, 295, 71, C("#f2c14e"))
	rect(img, 280, 74, 15, 1, C("#8f959e"))

	// ── 零食架：北臂西端上方的两层开放搁板（壁挂，竖撑落到台面）──
	rect(img, 238, 54, 1, 31, C("#9aa4ad")) // 西立柱（上到顶板下到台面）
	rect(img, 264, 54, 1, 31, C("#9aa4ad")) // 东立柱（对称落到台面，不悬空）
	rect(img, 238, 68, 26, 1, C("#9aa4ad")) // 下层板
	rect(img, 238, 54, 26, 1, C("#9aa4ad")) // 上层板
	// 上层：三包零食（品牌橙/红/黄，锯齿袋口）
	rect(img, 240, 50, 5, 4, C("#ff8800"))
	rect(img, 241, 49, 3, 1, C("#e0730a"))
	rect(img, 248, 50, 5, 4, markerRed)
	rect(img, 249, 49, 3, 1, C("#d03a35"))
	rect(img, 256, 50, 5, 3, C("#ffcf3f"))
	// 下层：三只储物罐
	rect(img, 241, 65, 4, 3, C("#a8c8e8"))
	rect(img, 242, 64, 2, 1, C("#8fb4da"))
	rect(img, 249, 65, 4, 3, C("#c9e6c8"))
	rect(img, 250, 64, 2, 1, C("#a8d8a6"))
	rect(img, 257, 65, 4, 3, C("#f2d9a8"))
	rect(img, 258, 64, 2, 1, C("#e0c288"))

	// ── 立式饮水机：北臂西端落地（蓝桶白身，冷热双龙头＋接水盘）──
	rect(img, 226, 86, 12, 4, C("#9fd6ee")) // 蓝水桶
	rect(img, 227, 87, 10, 2, C("#bfe3f2"))
	rect(img, 226, 90, 12, 18, C("#f0f3f6")) // 白机身
	rect(img, 226, 90, 12, 1, C("#d9dfe4"))
	px(img, 229, 93, markerBlue)             // 冷水龙头
	px(img, 232, 93, markerRed)              // 热水龙头
	rect(img, 227, 95, 10, 2, C("#e2e7ec"))  // 接水盘
	rect(img, 227, 99, 10, 6, C("#dfe4ea"))  // 储物格
	rect(img, 226, 105, 12, 3, C("#9aa4ad")) // 底座

	// ── 脚踏垃圾桶：台前西端（不锈钢，翻盖＋踏脚）──
	rect(img, 245, 104, 8, 10, C("#c8cfd6")) // 桶身
	rect(img, 245, 104, 8, 1, C("#aeb6be"))  // 桶沿
	rect(img, 244, 103, 10, 1, metalC)       // 翻盖
	rect(img, 248, 101, 2, 1, metalC)        // 盖把
	rect(img, 247, 114, 4, 1, metalC)        // 踏脚

	// ── 吧台凳×2：北臂南沿（台前喝咖啡的座；凳面贴着台底留 1px 缝）──
	drawBarStool(img, 280, 108)
	drawBarStool(img, 318, 108)
}

// drawBarStool paints one bar stool (legacy anchor = the seat's
// top-left; 8×11): warm wood round seat, chrome column and a splayed
// foot ring — the seat to drink coffee at, not work.
func drawBarStool(img *image.NRGBA, x, y int) {
	rect(img, x, y, 8, 2, woodHiC)    // 座面
	rect(img, x, y, 8, 1, woodC)      // 座沿
	rect(img, x+3, y+2, 2, 5, metalC) // 中柱
	rect(img, x+1, y+7, 6, 1, metalC) // 踏脚环（整圈）
	rect(img, x+1, y+8, 2, 3, metalC) // 外撇腿（2px 粗）
	rect(img, x+5, y+8, 2, 3, metalC)
}

// drawMeetingRoom paints the glass-walled meeting room (需求评审
// 会议): rug, wall display (dark — the canvas board paints the lit
// slide while a meeting is live), the conference table with its props,
// the north row's chair rears, and the partition walls with the door
// gap in the north run (the south row's chairs blit as ChairSprite on
// the board, y-sorted among the actors). All native-unit rects — one
// truth with the Meeting* accessors geom.json serves, so art and hit
// geometry can never drift apart.
func drawMeetingRoom(img *image.NRGBA) {
	// rug under everything
	r := meetRug
	rectN(img, r.Min.X-3, r.Min.Y-3, r.Dx()+6, r.Dy()+6, rugEdgeC)
	rectN(img, r.Min.X, r.Min.Y, r.Dx(), r.Dy(), rugC)
	// stitched border
	rectN(img, r.Min.X+5, r.Min.Y+5, r.Dx()-10, 2, rugEdgeC)
	rectN(img, r.Min.X+5, r.Max.Y-7, r.Dx()-10, 2, rugEdgeC)
	rectN(img, r.Min.X+5, r.Min.Y+5, 2, r.Dy()-12, rugEdgeC)
	rectN(img, r.Max.X-7, r.Min.Y+5, 2, r.Dy()-12, rugEdgeC)
	// fabric flecks (deterministic)
	for i := 0; i < 28; i++ {
		img.Set(r.Min.X+(i*53)%r.Dx(), r.Min.Y+(i*37)%r.Dy(), rugFleckC)
	}

	// the wall display, riding the north partition's inner face
	scr := meetScreen
	rectN(img, (scr.Min.X+scr.Max.X)/2-6, scr.Min.Y-4, 12, 3, metalC) // 壁挂支架
	rectN(img, scr.Min.X-3, scr.Min.Y-3, scr.Dx()+6, scr.Dy()+6, screenBezelC)
	rectN(img, scr.Min.X, scr.Min.Y, scr.Dx(), scr.Dy(), screenOffC)
	rectN(img, scr.Min.X+3, scr.Min.Y+2, scr.Dx()-6, 3, screenHiC) // 熄屏反光带
	img.Set(scr.Max.X-6, scr.Max.Y-4, screenLedC)                  // 待机红灯

	// 北排椅子（面向镜头那侧）：与南排同一把工位椅（drawTaskChair），
	// 刻意画在桌面之前——桌面盖住椅腿与滚轮，读作椅子推进了桌下，
	// 椅子的任何部分都不会落到桌面上
	for _, s := range MeetingSeats() {
		if s.Face != FaceDown {
			continue
		}
		drawTaskChair(img, s.Spot.X/S, s.Spot.Y/S-40)
	}

	// the conference table: oak top, front face, legs
	t := meetTable
	rectN(img, t.Min.X, t.Min.Y, t.Dx(), 8*S, deskTopC)
	rectN(img, t.Min.X, t.Min.Y+8*S, t.Dx(), 2, deskEdgeC)
	rectN(img, t.Min.X, t.Min.Y+8*S+2, t.Dx(), 6*S, tableFrontC)
	rectN(img, t.Min.X+2, t.Max.Y-2*S, 2*S, 2*S, legC)
	rectN(img, t.Max.X-2-2*S, t.Max.Y-2*S, 2*S, 2*S, legC)
	// 桌面小物：纸堆 + 文件夹 + 茶杯（评审会的水永远备着）
	rectN(img, t.Min.X+30, t.Min.Y+6, 24, 6, paperC)
	rectN(img, t.Min.X+30, t.Min.Y+12, 24, 2, paperShade)
	rectN(img, t.Min.X+34, t.Min.Y+4, 12, 3, folderC(0))
	rectN(img, t.Max.X-26, t.Min.Y+5, 6, 7, mugBlue)
	rectN(img, t.Max.X-20, t.Min.Y+7, 2, 2, C("#efe9dc")) // 杯柄

	// partition walls: north run split by the door gap, then west,
	// east and south runs closing the box
	w, gap := meetRoom, meetDoorGap
	drawPartition(img, image.Rect(w.Min.X, w.Min.Y, gap.Min.X, w.Min.Y+meetWallT))
	drawPartition(img, image.Rect(gap.Max.X, w.Min.Y, w.Max.X, w.Min.Y+meetWallT))
	drawPartition(img, image.Rect(w.Min.X, w.Min.Y+meetWallT, w.Min.X+meetWallT, w.Max.Y))
	drawPartition(img, image.Rect(w.Max.X-meetWallT, w.Min.Y+meetWallT, w.Max.X, w.Max.Y))
	drawPartition(img, image.Rect(w.Min.X, w.Max.Y-meetWallT, w.Max.X, w.Max.Y))
	// door posts and the threshold strip under the gap
	rectN(img, gap.Min.X-2, gap.Min.Y, 2, meetWallT, partEdgeC)
	rectN(img, gap.Max.X, gap.Min.Y, 2, meetWallT, partEdgeC)
	rectN(img, gap.Min.X, gap.Min.Y+meetWallT-3, gap.Dx(), 3, floorSeam)
}

// drawPartition paints one glass partition run: glazed fill, framed
// edges, a solid base band and two diagonal glints — reading as the
// same light office the desks live in.
func drawPartition(img *image.NRGBA, r image.Rectangle) {
	rectN(img, r.Min.X, r.Min.Y, r.Dx(), r.Dy(), partGlassC)
	rectN(img, r.Min.X, r.Min.Y, r.Dx(), 2, partEdgeC)   // 顶框
	rectN(img, r.Min.X, r.Max.Y-4, r.Dx(), 4, partBaseC) // 实心底座
	rectN(img, r.Min.X, r.Min.Y, 2, r.Dy(), partEdgeC)   // 侧框
	rectN(img, r.Max.X-2, r.Min.Y, 2, r.Dy(), partEdgeC)
	glint := C("#eef4f9")
	if r.Dy() > r.Dx() { // 竖墙：反光自上而下斜切
		for y := r.Min.Y + 6; y+8 < r.Max.Y-4; y += 28 {
			rectN(img, r.Min.X+4, y, 2, 7, glint)
			rectN(img, r.Min.X+8, y+4, 2, 5, glint)
		}
	} else { // 横墙：反光横向斜切
		for x := r.Min.X + 8; x+8 < r.Max.X-4; x += 34 {
			rectN(img, x, r.Min.Y+4, 7, 2, glint)
			rectN(img, x+4, r.Min.Y+6, 5, 2, glint)
		}
	}
}

// drawTaskChair paints the office's ONE task chair — the pods'
// ergonomic chair, rects verbatim from the old near-row fore copy —
// at legacy anchor (x, y) where the sitter's seat line is y+40. Every
// seat in the office (desks and meeting room alike) is this chair; the
// caller picks how it rides:
//   - back layer (pods' far row, meeting north row): drawn behind the
//     sitter, whose sprite covers it;
//   - ChairSprite (pods' near row, meeting south row): the standalone
//     sprite the canvas board y-sorts among the actors.
func drawTaskChair(img *image.NRGBA, x, y int) {
	rect(img, x-5, y+26, 10, 3, chairDkC) // 头枕
	rect(img, x-5, y+26, 10, 1, chairHiC)
	rect(img, x-7, y+29, 14, 8, chairC) // 网布高背
	rect(img, x-7, y+29, 14, 1, chairHiC)
	rect(img, x-5, y+31, 10, 1, chairDkC) // 网面横线
	rect(img, x-5, y+33, 10, 1, chairDkC)
	rect(img, x-7, y+36, 14, 1, chairDkC) // 腰靠
	// 扶手：贴着椅背两侧
	rect(img, x-9, y+29, 2, 6, chairDkC)
	rect(img, x+7, y+29, 2, 6, chairDkC)
	rect(img, x-9, y+29, 2, 1, chairHiC)
	rect(img, x+7, y+29, 2, 1, chairHiC)
	// 座垫前沿、中柱、五星脚与滚轮
	rect(img, x-6, y+37, 12, 3, chairDkC)
	rect(img, x-7, y+40, 14, 1, chairDkC)
	rect(img, x-1, y+40, 2, 2, legC)
	rect(img, x-5, y+42, 10, 1, legC)
	rect(img, x-5, y+43, 2, 1, metalC)
	rect(img, x-1, y+43, 2, 1, metalC)
	rect(img, x+3, y+43, 2, 1, metalC)
}

// drawDiploma paints one framed certificate (t_160, r_11 氛围墙挂)：
// warm-wood frame, cream face, a red ribbon bow and two「text」lines
// reading as an award from across the room. Legacy anchor = top-left,
// 14×11. Pure decoration — no geom, no nav footprint.
func drawDiploma(img *image.NRGBA, x, y int) {
	// frame
	rect(img, x, y, 14, 11, woodC)
	rect(img, x+1, y+1, 12, 9, C("#f5efdc")) // face
	// inner bevel line
	rect(img, x+1, y+1, 12, 1, C("#e4d9ba"))
	// ribbon bow (top-center) + tails
	px(img, x+6, y+2, markerRed)
	px(img, x+8, y+2, markerRed)
	px(img, x+7, y+3, C("#d03a35"))
	px(img, x+6, y+4, markerRed)
	px(img, x+8, y+4, markerRed)
	// two text lines (dark dashes)
	rect(img, x+3, y+6, 8, 1, C("#8a7a52"))
	rect(img, x+4, y+8, 6, 1, C("#b0a276"))
}

// drawTeamPhoto paints the framed team photo (t_160): dark frame, a
// sky backdrop and four dot-cow members with smile eyes — the studio's
// 合影. Legacy anchor = top-left, 12×10.
func drawTeamPhoto(img *image.NRGBA, x, y int) {
	// frame
	rect(img, x, y, 12, 10, C("#3a3f45"))
	rect(img, x+1, y+1, 10, 8, C("#cfe3f0")) // sky backdrop
	// four members: colored heads (dot) + smile eye
	cols := []color.RGBA{markerBlue, markerGreen, markerRed, C("#f2c14e")}
	for i, c := range cols {
		mx := x + 2 + i*3
		rect(img, mx, y+3, 2, 3, c)    // head
		px(img, mx, y+2, c)            // crown round
		px(img, mx, y+5, C("#3a3f45")) // smile eye
	}
	// ground line
	rect(img, x+1, y+8, 10, 1, C("#9db6c6"))
}

// drawShowcase paints the 收藏品货架 (t_188 荣誉墙改版): a floor-standing
// glass display case on the curtain wall's solid center column — the
// office's collectibles shelf for recent achievements, read as a third
// "pixel window" between the two curtain panes. Layout: charcoal frame,
// near-black interior lit by a warm top lamp, three glass decks of four
// slots each; every slot holds one standing trophy (drawTrophy) or an
// empty display peg. The roster itself is parameterized — the canvas
// board repaints the slots over this resting face from the achievement
// store (three sample trophies as the resting face). Legacy anchor =
// case's top-left (172, 30), 40×34: left edge flush with the west
// pane's jamb (paneL1), right edge flush with the file cabinet, base
// landing on the floor line (WallH) — same ground as the cabinet and
// the snack shelving it stands beside.
func drawShowcase(img *image.NRGBA) {
	x, y, w, h := 172, 30, 40, 34
	// 柜框（炭灰，左缘齐西窗边框、右缘齐文件柜，底落在地脚线）
	rect(img, x, y, w, h, C("#2c3138"))
	rect(img, x, y, w, 1, C("#3a3f45"))     // 框顶亮棱
	rect(img, x, y, 1, h, C("#3a3f45"))     // 框左亮棱
	rect(img, x+w-1, y, 1, h, C("#3a3f45")) // 框右亮棱
	// 柜内近黑背板
	rect(img, x+1, y+1, w-2, h-2, C("#1c1f25"))
	// 顶灯暖光：灯条＋一行暖晕（三角洲展柜的射灯语言）
	rect(img, x+17, y+1, 6, 1, C("#f2c14e"))
	rect(img, x+1, y+2, w-2, 1, C("#332c1d"))
	// 玻璃斜向反光（暗斜线两道＋一道亮棱，读作柜面玻璃）
	for d := 0; d < 8; d++ {
		px(img, x+27+d, y+5+d, C("#262b33"))
	}
	for d := 0; d < 6; d++ {
		px(img, x+32+d, y+5+d, C("#262b33"))
	}
	px(img, x+5, y+5, C("#3d454f"))
	px(img, x+6, y+6, C("#3d454f"))
	// 三层玻璃货架（上层亮棱＋板身），各带 4 个展位
	for _, sy := range []int{y + 11, y + 21, y + 31} {
		rect(img, x+1, sy, w-2, 1, C("#cfe3f0"))
		rect(img, x+1, sy+1, w-2, 1, C("#9db6c6"))
	}
	// 展位：首层金/银/铜三枚样例奖杯（静息面——运行时由成就数据重绘），
	// 空位给展示底座（有位置感，不装满）
	for i := 0; i < 4; i++ {
		sx := x + 2 + i*9
		if i < 3 {
			drawTrophy(img, sx, y+3, trophyPalettes[i])
			drawTrophy(img, sx, y+13, trophyPalettes[i])
			drawTrophy(img, sx, y+23, trophyPalettes[i])
		} else {
			rect(img, sx+2, y+10, 3, 1, C("#3d454f"))
			rect(img, sx+2, y+20, 3, 1, C("#3d454f"))
			rect(img, sx+2, y+30, 3, 1, C("#3d454f"))
		}
	}
	// 落地踢脚（压地脚线的暗带——柜子站住地面）
	rect(img, x+1, y+h-1, w-2, 1, C("#101318"))
}

// trophyPalettes：奖杯三档色（金/银/铜）——与前端 achievements.js 的
// MEDAL_PAL 孪生，跨端同源。
var trophyPalettes = [3][2]color.RGBA{
	{C("#f2c14e"), C("#b8860b")}, // 金杯：亮金面＋暗金边
	{C("#c8cfd6"), C("#8f959e")}, // 银杯
	{C("#e0a03a"), C("#a05a2a")}, // 铜杯
}

// drawTrophy paints one standing trophy (t_188 荣誉墙改版): a wide cup
// bowl with ear handles, a tapered foot and a two-step base — the
// collectible on the showcase shelf, kept mostly in the palette's main
// color so it pops off the case's near-black interior. Legacy anchor =
// top-left, 7×8.
func drawTrophy(img *image.NRGBA, x, y int, pal [2]color.RGBA) {
	// 杯身：口沿→最宽腹线（连耳）→收腹→颈→柄→座张口
	rect(img, x+1, y, 5, 1, pal[0])
	rect(img, x, y+1, 7, 1, pal[0])
	rect(img, x+1, y+2, 5, 1, pal[0])
	rect(img, x+2, y+3, 3, 1, pal[0])
	px(img, x+3, y+4, pal[0])
	rect(img, x+2, y+5, 3, 1, pal[0])
	// 杯耳两侧点暗，底座两级收暗
	px(img, x, y+2, pal[1])
	px(img, x+6, y+2, pal[1])
	rect(img, x+2, y+6, 3, 1, pal[1])
	rect(img, x+1, y+7, 5, 1, pal[1])
	// 口沿高光
	px(img, x+2, y, C("#ffffff"))
}
