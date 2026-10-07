// desks.js — the desk pool (seating.go's port): spots from geom,
// occupancy + release hysteresis, busy-first row preference, idle
// seating. Pure seat bookkeeping — the board decides who is busy, this
// decides where they sit.

const DESK_HOLD = 30; // release hysteresis, seconds

export class DeskPool {
  constructor() { this.use = new Map(); this.hold = new Map(); this.spots = []; }

  /** Lay the seat anchors once — the office scene is a fixed size.
   *  Each spot carries its sitter's facing (0 faces the camera, 1
   *  faces away) so seated actors turn toward their desk. */
  layout(geom) {
    if (this.spots.length === (geom?.desks.length ?? 0)) return this.spots;
    this.spots = (geom?.desks ?? []).map((d) => ({ x: d.spot[0], y: d.spot[1], face: d.face ?? 1 }));
    return this.spots;
  }

  reset() { this.use.clear(); this.hold.clear(); this.spots = []; }

  /** Drop every sitter not in keep; returns the freed [{idx, name}]. */
  evictSitters(keep) {
    const freed = [];
    for (const [idx, name] of this.use) {
      if (keep.has(name)) continue;
      this.use.delete(idx);
      freed.push({ idx, name });
    }
    return freed;
  }

  /** Seat a busy member: keep the last desk inside hysteresis, else the
   *  wall row first (nearest by manhattan), then the front rows. */
  claimBusy(name, x, y, last, clock) {
    const spots = this.spots;
    if (last >= 0 && (this.hold.get(last) ?? 0) > clock) {
      if (last < spots.length && !this.use.has(last)) {
        this.use.set(last, name);
        return { idx: last, spot: spots[last] };
      }
    }
    if (!spots.length) return null;
    const free = (i) => !this.use.has(i) && (this.hold.get(i) ?? 0) <= clock;
    let wallY = Infinity;
    for (const s of spots) if (s.y < wallY) wallY = s.y;
    const pick = (wallRow) => {
      let best = -1, bestD = Infinity;
      spots.forEach((s, i) => {
        if (!free(i) || ((s.y === wallY) !== wallRow)) return;
        const d = Math.abs(s.x - x) + Math.abs(s.y - y);
        if (d < bestD) { best = i; bestD = d; }
      });
      return best;
    };
    let best = pick(true); // the wall row first…
    if (best < 0) best = pick(false); // …then the front rows
    if (best < 0) return null;
    this.use.set(best, name);
    return { idx: best, spot: spots[best] };
  }

  /** Seat an idle member: first free spot, no preference. */
  claimIdle(name, clock) {
    for (let i = 0; i < this.spots.length; i++) {
      if (this.use.has(i) || (this.hold.get(i) ?? 0) > clock) continue;
      this.use.set(i, name);
      return { idx: i, spot: this.spots[i] };
    }
    return null;
  }

  release(idx, clock) { this.use.delete(idx); this.hold.set(idx, clock + DESK_HOLD); }

  /** Forget a member entirely (both the seat and its hysteresis hold). */
  drop(name) {
    for (const [idx, n] of this.use) {
      if (n === name) { this.use.delete(idx); this.hold.delete(idx); }
    }
  }
}
