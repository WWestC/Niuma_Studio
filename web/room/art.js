// art.js — the office board's asset kit (v2 P6): one geom.json + two
// PNG layers at the office scene's one fixed size, one avatar atlas
// row per member. Every request is content-addressed (/art/*?…,
// server-side memoized), so after the first hit the browser cache
// carries the rest. The office has no computers — a working desk is
// told by its lamp, which the board paints lit over the back layer's
// unlit brass (roomview.js).

// The office scene's fixed size in native px — the mirror of pixart's
// RoomW/RoomH (the art's 384×260 legacy design rect × the ×2 rescale).
// The board letterboxes this scene into its pane; the server renders
// every layer at exactly this size, whatever the pane does.
export const SCENE_W = 768;
export const SCENE_H = 520;

// Atlas frame order — byte-stable against pixart.AtlasOrder (the
// server lays the row in this exact order).
export const FRAME = {
  downA: 0, downB: 1, downBlink: 2,
  upA: 3, upB: 4,
  leftA: 5, leftB: 6,
  rightA: 7, rightB: 8,
};
export const AVATAR_FRAMES = 9;

function loadImage(url) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error(`art load failed: ${url}`));
    img.src = url;
  });
}

// todOf maps the local clock onto the curtain wall's view phase — the
// office window follows the real time: dawn 05:00–08:00, day
// 08:00–17:00, dusk 17:00–19:30, night the rest.
export function todOf(d = new Date()) {
  const m = d.getHours() * 60 + d.getMinutes();
  if (m >= 300 && m < 480) return 'dawn';
  if (m >= 480 && m < 1020) return 'day';
  if (m >= 1020 && m < 1170) return 'dusk';
  return 'night';
}

export class ArtKit {
  // ARTV busts the layers' content-addressed URLs when the generators
  // change — v2 added the wall's ceiling fade, v3 made its source
  // color page-driven (?top=, the theme's --titlebar), v4 restyled
  // the scene into the light office, v5 rebuilt the furniture (oak
  // desks, slim monitors, steel-blue chairs), v6 retired every
  // computer — desks wear brass lamps (unlit; the board paints the
  // working glow), papers, pen cups and plants, and the lounge gained
  // rugs + a floor lamp. v7 fixed the scene: one RoomW×RoomH render,
  // desk rows no longer adapt to the viewport. v8 rebuilt the floor
  // into 大厂-style face-to-face 2×2 bench islands with ergonomic
  // chairs — the wall row sits facing the camera, the front row shows
  // its chair backs — and every seat's facing now rides geom (`face`).
  // v9 cleared the lounge: the meeting table and the sofa (with their
  // rugs) are gone; only the corner floor lamp remains. v10 removed
  // the floor lamp too — the lounge corner is now bare carpet. v11
  // gave the camera-facing chairs a full mesh backrest (the old side
  // slivers read as goalposts on empty desks). v12 fixed the layers'
  // sprite compositing: Blit used to copy sprites' transparent margins
  // raw, punching page-background holes around the plants, dispenser,
  // standby sign and cabinet pot — they now composite properly. v13
  // built the meeting room (需求评审会议): a glass-walled box in the
  // south-east corner with a door gap, a conference table and a wall
  // display that ships dark — the board paints it lit while a meeting
  // is live (the desk lamps' own dark-base/lit-glow contract). v14
  // seated the room: six boardroom chairs — north-row rears on the
  // back layer, south-row full chairs on the fore layer (the pods'
  // own sit-into contract). v15 lowered every chair into a low
  // boardroom chair: the north row's backs now clear the wall screen
  // entirely (the tall slab backs used to cover it). v16 strengthened
  // the north row's seated read: a seat-cushion front edge across the
  // sitter's lap plus wider armrest tabs (the pods' sit-into language).
  // v17 unified the furniture: the south row is the office task chair
  // VERBATIM (pods' near-row rects, armrests and five-star base
  // included), the north row the same family at 28px wide with flush
  // armrests — no more floating tabs. v18 finished the job: ONE
  // drawTaskChair paints all six chairs; the north row is drawn
  // before the table so the tabletop hides its legs — the chairs read
  // as tucked under the table, nothing lands on the surface. v19 made
  // it the office's ONLY chair: the pods' far-row and near-row blocks
  // now call drawTaskChair too, so every seat — desks and meeting
  // room alike — is the same ergonomic chair. v20 pulled the
  // away-facing chairs (pods' near row + meeting south row) out of the
  // static fore layer — a layer paints over EVERY actor, so anyone
  // walking south of a chair wore it as a torso — and serves them as
  // one chair.png the board y-sorts among the actors (sitter covered,
  // passer-by on top). v21 separated the nav grid's collision from the
  // sitter's art zone: the far row's blocked band used to start at the
  // seated head line, walling off the open aisle between the two
  // island banks — desks now export a furniture-footprint block rect
  // (chair headrest → desk edge) that nav.js walks around. v23 built
  // the pantry (茶水间): a counter run with coffee machine and kettle
  // along the east wall above the meeting room, plus a café table with
  // two wooden chairs — idle members take coffee breaks there (t_103).
  // v24 repainted the pantry in the office's legacy-unit chunky
  // language (the rectN fine-line pass read off-key against the ×2
  // art): warm rug anchoring the corner, bistro table with the meeting
  // table's own depth formula, profile wooden chairs. v25 rebuilt it
  // per the owner's review: one long L-shaped modern pantry counter
  // (城市办公楼茶水间) — quartz tops, matte flat-panel cabinets,
  // integrated steel fridge, inset sink with gooseneck faucet, cup
  // dispenser, overhead snack shelving and two bar stools; no rug, no
  // café table. v26 turned the back wall into a CBD curtain wall: two
  // floor-to-ceiling panes whose city view follows the real clock
  // (?tod=day|dusk|night|dawn — the board re-derives the phase and
  // swaps the back layer on the crossing), the whiteboard now a
  // rolling stand at the office's bottom-left corner, and the framed
  // poster leaning on the meeting room's glass. v27 stocked the pantry
  // to a real 城市办公楼 spec (owner's review): standing water
  // dispenser at the counter's west end, microwave on the worktop,
  // tissue roll, fruit bowl, stacked saucers, a floor pedal bin, and a
  // chalk menu board on the wall. v28 dropped the snack shelf's east
  // post down to the countertop (the right side read floating). v29
  // rebuilt the cabinet runs to real millwork: the north arm's five
  // slab doors with edge reveals and discrete metal pulls, the fridge
  // as a full-height integrated double-door unit, the east arm as four
  // tall-door cabinets matching the north run's language. v30 built the
  // 大型打印机 (r_03): a three-deck floor MFP flanking the whiteboard's
  // east side — scanner lid + output tray with a resident sheet, the
  // control panel band with its dark LED seat, the paper cabinet. The
  // old desk printer in the north-east corner retired. v31 lived in
  // the pantry per the owner's mock (pantry_mock_v2.png): the east
  // arm's four closed tall doors became an open storage unit — oak
  // shelves stacked with colorful boxes, paper bags and jars (the
  // closed run read as a gray wall); the fridge door wears round
  // magnets and a sticky note; the sink bay dropped its slab door for
  // a three-drawer stack with bar pulls; a lidded red/blue jar pair
  // joined the worktop. v33 upsized the printer to the whiteboard's
  // own height (86 native) with its base aligned to the board's
  // casters (y=494) — a real floor-standing MFP's heft, with front
  // paper drawers and a side cubby. v34 halved its height (42 native)
  // per the owner's call — base still flush with the board's casters.
  // v32 fixed the printer's coordinates: the
  // r03 spec's "native (103,204)" was legacy numbers — the board
  // actually stands at native (98,408)-(202,494), and the printer had
  // landed mid-floor between the desk banks. It now flanks the
  // whiteboard's east side for real: native (206,408)-(232,452). v35
  // replaced the t_188 honor wall's buried medal rail with the
  // collectibles showcase — a wall-hung glass case on the north
  // column (native (344,60)-(424,116)): charcoal frame, warm top
  // lamp, two glass decks, trophy slots the board repaints from the
  // achievement store (roomview paintShowcase). v36 grounded the case —
  // 40×34 down to the floor line with a third deck (12 slots) and a
  // kick base, standing on the same ground as the file cabinet.

  static ARTV = 36;

  constructor() {
    this.geom = null;                 // /art/geom.json payload
    this.back = null;                 // office layer behind the actors
    this.fore = null;                 // transparent desk fronts over the actors
    this.chair = null;                // the away-facing chair sprite (y-sorted)
    this.avatars = new Map();         // "shirt|hair" → HTMLImageElement
    this.tod = null;                  // the curtain wall's phase the back layer rendered
    this.qTop = '#f5f6f7';            // the ceiling fade's source, kept for re-fetches
  }

  /**
   * Load the fixed-size set (once — there is no per-viewport layer).
   * Resolves with this; a failure leaves the layers unset (the board
   * keeps retrying on resize).
   */
  async load() {
    // the wall's ceiling fade starts from the page chrome's color
    // (--titlebar, same one the native window is painted) — the room
    // melts into whatever theme the page currently wears
    let top = '#f5f6f7';
    try {
      const v = getComputedStyle(document.documentElement).getPropertyValue('--titlebar').trim();
      if (/^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(v)) top = v;
    } catch { /* computed style unavailable — default light */ }
    this.qTop = top;
    this.tod = todOf();
    const q = `v=${ArtKit.ARTV}&top=${encodeURIComponent(top)}&tod=${this.tod}`;
    const [geom, back, fore, chair] = await Promise.all([
      fetch(`/art/geom.json?${q}`).then((r) => r.json()),
      loadImage(`/art/room-back.png?${q}`),
      loadImage(`/art/room-fore.png?${q}`),
      loadImage(`/art/chair.png?${q}`),
    ]);
    this.geom = geom;
    this.back = back; this.fore = fore; this.chair = chair;
    return this;
  }

  /**
   * One member's atlas row (loads once, cached by colors). The image
   * may still be decoding on first ask — ready() gates the draw.
   * @param {string} shirt @param {string} hair
   */
  atlas(shirt, hair, who) {
    // r_05（t_126/t_127）：带成员名走分层签名——终身层（发型/肤色/
    // 饰品）查服务端 staffing 档案，上衣按 day 日期哈希换装（同人同
    // 日同衣）。无 who（老调用）保持旧两维 URL，行为不变。
    const day = new Date().toISOString().slice(0, 10);
    const key = who ? `who:${who}|${day}` : `${shirt}|${hair}`;
    let img = this.avatars.get(key);
    if (!img) {
      img = new Image();
      img.src = who
        ? `/art/avatar.png?who=${encodeURIComponent(who)}&day=${day}`
        : `/art/avatar.png?shirt=${encodeURIComponent(shirt)}&hair=${encodeURIComponent(hair)}`;
      this.avatars.set(key, img);
    }
    return img;
  }

  /**
   * Re-derive the curtain wall's phase from the real clock (the board
   * ticks this on a slow beat). Crossing a boundary fetches the next
   * room-back — one content-addressed image per phase, so every swap
   * after the first sunset is a cache hit. The old layer keeps showing
   * until the new one decodes (no flash of bare wall); a failed fetch
   * resets the phase so the next tick retries.
   */
  tickTod() {
    if (!this.geom) return;
    const tod = todOf();
    if (tod === this.tod) return;
    this.tod = tod;
    const q = `v=${ArtKit.ARTV}&top=${encodeURIComponent(this.qTop)}&tod=${tod}`;
    loadImage(`/art/room-back.png?${q}`)
      .then((img) => { this.back = img; })
      .catch(() => { this.tod = null; });
  }

  ready() { return !!(this.geom && this.back && this.fore && this.chair); }
}
