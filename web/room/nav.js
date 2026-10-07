// nav.js — the office floor's walk grid: every desk, partition wall,
// conference table and floor prop geom lists becomes a blocked cell,
// and A* finds the way around them. This is the board's side of the
// "the layers' drawing and the hit geometry stay one truth" contract —
// the grid blocks exactly what the art painted. Pure read-only
// geometry: actors and the board ask it free()/path() and nothing
// else.

const CELL = 4;    // native px per grid cell — fine enough for the aisles the art leaves
const INFLATE = 4; // obstacles grow one cell so feet clear desk corners —
                   // any fatter and the blobs swallow the open gaps the
                   // art leaves between and around the islands
const DRIFT = 30;  // nearest-free search radius, in cells (goal/start relaxation)
// 软阻挡（r_07 t_138）：地面垃圾的路径代价——litter 格不进 blocked（三
// 边界天然免疫：goal 不被挪、站立不需 escape、铺满仍可解），只在 A* 展开
// 时加惩罚。penalty 由 energy.js 的 EPARAMS 注入（调参区一处真源）。
let LITTER_PENALTY = 6;
// PEOPLE_PENALTY：挡路行人的软代价（r_12 t_163——垃圾 6 的轻档）
let PEOPLE_PENALTY = 3;

/** setPeoplePenalty 配置人群软代价（调参区；0＝关）。 */
export function setPeoplePenalty(v) { PEOPLE_PENALTY = v || 0; }

/** setLitterPenalty 配置软阻挡代价（调参区调用；0＝关软阻挡）。 */
export function setLitterPenalty(v) { LITTER_PENALTY = v || 0; }

export class Nav {
  /**
   * @param {object} geom the /art/geom.json payload
   * @param {{minX:number,minY:number,maxX:number,maxY:number}} bounds the feet clamps
   */
  constructor(geom, bounds) {
    this.cols = Math.ceil(geom.w / CELL);
    this.rows = Math.ceil(geom.h / CELL);
    const blocked = new Uint8Array(this.cols * this.rows);
    const rects = [];
    for (const d of geom.desks || []) {
      // block is the furniture footprint (desk + chair band); the full
      // art rect reaches up to the far sitters' head line, 12 legacy px
      // of open aisle above the chairs that must stay walkable
      rects.push(d.block || [d.x, d.y, d.w, d.h]);
    }
    for (const p of geom.props || []) rects.push(p);
    // 茶水间家具（茶水台/咖啡馆方桌/木椅）——与桌子同一张碰撞网格，
    // 画了什么就挡什么
    if (geom.pantry) {
      for (const p of geom.pantry.props || []) rects.push(p);
    }
    const m = geom.meet;
    if (m) {
      const [rx, ry, rw, rh] = m.room;
      const t = m.wallT || 12;
      const [gx, gy, gw] = m.gap || [0, 0, 0];
      if (gw > 0) {
        // north run split around the door opening — the room's one seam
        rects.push([rx, ry, gx - rx, t], [gx + gw, ry, rx + rw - gx - gw, t]);
      } else {
        rects.push([rx, ry, rw, t]);
      }
      rects.push([rx, ry + rh - t, rw, t]);               // south run
      rects.push([rx, ry + t, t, rh - t * 2]);            // west run
      rects.push([rx + rw - t, ry + t, t, rh - t * 2]);   // east run
      if (m.table) rects.push(m.table);
    }
    for (const [x, y, w, h] of rects) {
      this.stamp(blocked, x - INFLATE, y - INFLATE, w + INFLATE * 2, h + INFLATE * 2);
    }
    // the bounds are the feet clamps — a cell whose center lies outside
    // them can never be stood on, so it is not walkable either
    for (let cy = 0; cy < this.rows; cy++) {
      for (let cx = 0; cx < this.cols; cx++) {
        const px = cx * CELL + CELL / 2, py = cy * CELL + CELL / 2;
        if (px < bounds.minX || px > bounds.maxX || py < bounds.minY || py > bounds.maxY) {
          blocked[cy * this.cols + cx] = 1;
        }
      }
    }
    this.blocked = blocked;
  }

  stamp(blocked, x, y, w, h) {
    const x0 = Math.max(0, Math.floor(x / CELL)), x1 = Math.min(this.cols, Math.ceil((x + w) / CELL));
    const y0 = Math.max(0, Math.floor(y / CELL)), y1 = Math.min(this.rows, Math.ceil((y + h) / CELL));
    for (let cy = y0; cy < y1; cy++) blocked.fill(1, cy * this.cols + x0, cy * this.cols + x1);
  }

  idx(x, y) {
    const cx = Math.floor(x / CELL), cy = Math.floor(y / CELL);
    if (cx < 0 || cy < 0 || cx >= this.cols || cy >= this.rows) return -1;
    return cy * this.cols + cx;
  }

  /** Can a pair of feet stand on this point? (bounds + grid) */
  free(x, y) {
    const i = this.idx(x, y);
    return i >= 0 && !this.blocked[i];
  }

  /** Every 2px sample along the segment stands on a free cell. */
  clearLine(x0, y0, x1, y1) {
    const dx = x1 - x0, dy = y1 - y0;
    const n = Math.max(1, Math.ceil(Math.hypot(dx, dy) / 2));
    for (let i = 0; i <= n; i++) {
      if (!this.free(x0 + (dx * i) / n, y0 + (dy * i) / n)) return false;
    }
    return true;
  }

  /**
   * 拉直采样（t_189）：clearLine 的行人版——free 之外还要求沿途不踩
   * peopleAt 标记格（坐定者的 28px 盘）。终点豁免：采样不含末点——
   * 路径终点头一个格常在盘内（会议座就钉在桌沿盘边），进盘的最后一
   * 跳是合法到达；只有「穿过盘去更远处」才断拉直。无标记时与
   * clearLine 完全同价。
   */
  clearPull(x0, y0, x1, y1) {
    const dx = x1 - x0, dy = y1 - y0;
    const n = Math.max(1, Math.ceil(Math.hypot(dx, dy) / 2));
    for (let i = 1; i < n; i++) {
      const x = x0 + (dx * i) / n, y = y0 + (dy * i) / n;
      if (!this.free(x, y) || this.peopleAt(this.idx(x, y))) return false;
    }
    return true;
  }

  /**
   * A* from (sx,sy) to (tx,ty), string-pulled into a short waypoint
   * list. A start or goal that lands on furniture (desk and meeting
   * spots sit right at the art's edges) relaxes to the nearest free
   * cell — the caller walks the last few px straight, the sanctioned
   * "arrive at the desk" hop. Unreachable or degenerate → [] (the
   * caller falls back to its straight walk).
   */
  path(sx, sy, tx, ty) {
    let s = this.idx(sx, sy), g = this.idx(tx, ty);
    if (s < 0 || g < 0) return [];
    if (this.blocked[g]) g = this.nearestFree(g);
    if (this.blocked[s]) s = this.nearestFree(s);
    if (g < 0 || s < 0 || s === g) return [];
    const raw = this.astar(s, g);
    if (!raw || !raw.length) return [];
    // string pull: from the current point, jump to the farthest
    // waypoint still in a clear straight line — 拉直走 clearPull（行
    // 人格断拉直，t_189）：A* 绕出的车道不能被拉直摺回贴身直线
    const pts = [{ x: sx, y: sy }, ...raw];
    const out = [];
    let i = 0;
    while (i < pts.length - 1) {
      let j = pts.length - 1;
      while (j > i + 1 && !this.clearPull(pts[i].x, pts[i].y, pts[j].x, pts[j].y)) j--;
      out.push(pts[j]);
      i = j;
    }
    return out;
  }

  /** Nearest free cell by grid steps (BFS over blocked cells too). */
  nearestFree(i) {
    const { cols, rows, blocked } = this;
    const seen = new Uint8Array(cols * rows);
    let q = [i];
    seen[i] = 1;
    for (let depth = 0; depth < DRIFT && q.length; depth++) {
      const next = [];
      for (const cur of q) {
        if (!blocked[cur]) return cur;
        const cx = cur % cols, cy = (cur / cols) | 0;
        for (let dy = -1; dy <= 1; dy++) {
          for (let dx = -1; dx <= 1; dx++) {
            if (!dx && !dy) continue;
            const nx = cx + dx, ny = cy + dy;
            if (nx < 0 || ny < 0 || nx >= cols || ny >= rows) continue;
            const ni = ny * cols + nx;
            if (seen[ni]) continue;
            seen[ni] = 1;
            next.push(ni);
          }
        }
      }
      q = next;
    }
    return -1;
  }

  /**
   * The nearest walkable point to stand on — the escape target for a
   * body that finds itself inside a blocked cell (a corner cut or a
   * crowd squeeze). Walking HERE leaves the furniture by the shortest
   * line, never across it.
   */
  escape(x, y) {
    const i = this.idx(x, y);
    if (i < 0) return { x, y };
    if (!this.blocked[i]) return { x, y };
    const j = this.nearestFree(i);
    if (j < 0) return { x, y };
    return { x: (j % this.cols) * CELL + CELL / 2, y: ((j / this.cols) | 0) * CELL + CELL / 2 };
  }

  /** litterAt reports the soft-blocking litter mark of a cell index —
   *  the board feeds this from its energy engine's litter list; default
   *  0 (no litter on the floor). */
  litterAt(i) { return 0; }

  /** peopleAt reports the crowd-avoidance mark of a cell (r_12 t_163):
   *  the board feeds standing walkers' positions so re-routed paths
   *  swing around them (soft cost, lighter than litter); default 0. */
  peopleAt(i) { return 0; }

  /** Plain grid A*, 8-dir with no corner cutting; cell centers out. */
  astar(s, g) {
    const { cols, rows, blocked } = this;
    const n = cols * rows;
    const gScore = new Float32Array(n).fill(Infinity);
    const came = new Int32Array(n).fill(-1);
    const closed = new Uint8Array(n);
    const gx = g % cols, gy = (g / cols) | 0;
    const h = (i) => {
      const dx = Math.abs((i % cols) - gx), dy = Math.abs(((i / cols) | 0) - gy);
      return (dx > dy ? dx - dy : dy - dx) + 1.4142 * Math.min(dx, dy);
    };
    const heap = [];
    const push = (f, i) => {
      heap.push([f, i]);
      let c = heap.length - 1;
      while (c > 0) {
        const p = (c - 1) >> 1;
        if (heap[p][0] <= heap[c][0]) break;
        [heap[p], heap[c]] = [heap[c], heap[p]];
        c = p;
      }
    };
    const pop = () => {
      const top = heap[0];
      const last = heap.pop();
      if (heap.length) {
        heap[0] = last;
        let c = 0;
        for (;;) {
          const l = c * 2 + 1, r = l + 1;
          let m = c;
          if (l < heap.length && heap[l][0] < heap[m][0]) m = l;
          if (r < heap.length && heap[r][0] < heap[m][0]) m = r;
          if (m === c) break;
          [heap[m], heap[c]] = [heap[c], heap[m]];
          c = m;
        }
      }
      return top;
    };
    gScore[s] = 0;
    push(h(s), s);
    const DIRS = [[1, 0, 1], [-1, 0, 1], [0, 1, 1], [0, -1, 1],
      [1, 1, 1.4142], [1, -1, 1.4142], [-1, 1, 1.4142], [-1, -1, 1.4142]];
    while (heap.length) {
      const [, cur] = pop();
      if (cur === g) break;
      if (closed[cur]) continue;
      closed[cur] = 1;
      const cx = cur % cols, cy = (cur / cols) | 0;
      for (const [dx, dy, cost] of DIRS) {
        const nx = cx + dx, ny = cy + dy;
        if (nx < 0 || ny < 0 || nx >= cols || ny >= rows) continue;
        const ni = ny * cols + nx;
        if (blocked[ni] || closed[ni]) continue;
        if (dx && dy && (blocked[cy * cols + nx] || blocked[ny * cols + cx])) continue;
        // 软阻挡：垃圾格加惩罚代价（不 block——路仍通，只是不划算）
        const lit = this.litterAt(ni) ? LITTER_PENALTY : 0;
        // 人群软代价（r_12 t_163）：站着的行人格同样加惩罚——重算的
        // 路径自然绕开挡路者（比垃圾轻一档：人是暂时的、垃圾要等保洁）
        const ppl = this.peopleAt(ni) ? PEOPLE_PENALTY : 0;
        const ng = gScore[cur] + cost + lit + ppl;
        if (ng < gScore[ni]) {
          gScore[ni] = ng;
          came[ni] = cur;
          push(ng + h(ni), ni);
        }
      }
    }
    if (came[g] < 0) return null;
    const pts = [];
    for (let i = g; i !== s && i >= 0; i = came[i]) {
      pts.push({ x: (i % cols) * CELL + CELL / 2, y: ((i / cols) | 0) * CELL + CELL / 2 });
    }
    pts.reverse();
    return pts;
  }
}
