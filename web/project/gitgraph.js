// gitgraph.js — 提交图布局纯逻辑（v2.8，GitLens 式树形提交图的几何
// 半场）：输入拓扑序提交流（--all --topo-order，新→旧），输出每笔落
// 哪条泳道（lane）＋连线表（edge），渲染层只管把数字变成 SVG。零
// DOM、零 fetch——node --test 直接咬（web/room 的纯逻辑纪律同源）。
//
// 算法是经典泳道分配：active 表登记「父提交 SHA → 它将来出现的
// lane」，逐行消费——本行 SHA 在表里就落那条道并销账；不在（分支头
// 或窗口顶）就取空道或开新道。merge 的 parent[0] 延续本道、其余父
// 开新道向下分叉；两子共一父时后到者汇入先占的道（后到者自己的道
// 就此终结）。道用完归还空池、小号优先复用，图不横向无限膨胀。

/** 行高与泳道几何（px）——渲染层与测试共饮的常量。 */
export const ROW_H = 30;
export const LANE_W = 14;
export const LANE_X0 = 12; // lane 0 中心的 x 偏移（留出首道与容器的呼吸）

/** 泳道配色（8 色循环）：lane 序号 → 16 进制色。GitLens 的多彩线。 */
const LANE_COLORS = [
  '#4f8fef', '#e5a13c', '#5cb87a', '#d970b8',
  '#54b8c5', '#c56ac5', '#a3b545', '#d97b66',
];
export function laneColor(lane) { return LANE_COLORS[((lane % LANE_COLORS.length) + LANE_COLORS.length) % LANE_COLORS.length]; }

/**
 * layoutGraph — 泳道分配与连线表。
 * @param {{sha: string, parent_shas?: string[], parents?: number}[]} commits 拓扑序（新→旧）
 * @returns {{
 *   laneOf: Map<string, number>, maxLane: number,
 *   edges: Array<{fromIdx: number, toIdx: number, fromLane: number, toLane: number, colorLane: number}>,
 * }} toIdx=-1 是窗口截断线（父在窗口外，画到行底淡出）
 */
export function layoutGraph(commits) {
  const idxOf = new Map(commits.map((c, i) => [c.sha, i]));
  const laneOf = new Map();       // sha → 本行落点
  const active = new Map();       // 父 sha → 它将出现的 lane（向下延伸的线）
  const free = [];                // 空闲 lane 池（小号优先）
  let maxLane = -1;
  const take = () => {
    if (free.length) { free.sort((a, b) => a - b); return free.shift(); }
    maxLane += 1;
    return maxLane;
  };
  const give = (l) => { if (l >= 0) free.push(l); };
  const edges = [];
  commits.forEach((c, i) => {
    let lane;
    if (active.has(c.sha)) {          // 这条线延伸到了本行
      lane = active.get(c.sha);
      active.delete(c.sha);
    } else {                          // 分支头 / 窗口顶：取空道或开新道
      lane = take();
    }
    laneOf.set(c.sha, lane);
    const inner = (c.parent_shas || []).filter((p) => idxOf.has(p));
    if (!inner.length) {
      // 根提交：线到头；有窗口外父则记一条截断淡出线
      if ((c.parent_shas || []).length) {
        edges.push({ fromIdx: i, toIdx: -1, fromLane: lane, toLane: lane, colorLane: lane });
      }
      give(lane);
      return;
    }
    for (const p of inner) {
      // 父已被占（两子共一父）＝汇入别道；否则 parent[0] 延续本道、
      // 其余父各开新道向下分叉。
      const toLane = active.has(p) ? active.get(p) : (p === inner[0] ? lane : take());
      active.set(p, toLane);
      edges.push({ fromIdx: i, toIdx: idxOf.get(p), fromLane: lane, toLane, colorLane: toLane });
    }
    // 本道没被主线延续（merge 汇入别道），归还空池
    if (active.get(inner[0]) !== lane) give(lane);
  });
  return { laneOf, maxLane: Math.max(maxLane, 0), edges };
}

/**
 * edgePath — 一条连线的 SVG path（渲染层用）。同道竖线；跨道贝塞尔
 * 圆滑斜线（GitLens 的分叉/汇入弧线）。y 以行序号 × ROW_H 计。
 */
export function edgePath(e) {
  const x1 = LANE_X0 + e.fromLane * LANE_W;
  const x2 = LANE_X0 + e.toLane * LANE_W;
  const y1 = e.fromIdx * ROW_H + ROW_H / 2;
  const y2 = (e.toIdx < 0 ? e.fromIdx + 1 : e.toIdx) * ROW_H + ROW_H / 2;
  if (e.fromLane === e.toLane) return `M${x1} ${y1}L${x1} ${y2}`;
  const my = (y1 + y2) / 2;
  return `M${x1} ${y1}C${x1} ${my} ${x2} ${my} ${x2} ${y2}`;
}

/**
 * refsBySHA — 分支表 → 每个 HEAD 提交贴哪些分支徽章（GitLens 的
 * refs 列：分支名贴在它指向的提交行上）。组内排序：当前分支最前、
 * 本地其次、远端按名——一行多枝时最重要的先看到。
 * @param {{name: string, sha: string, current?: boolean, remote?: string}[]} branches
 * @returns {Map<string, Array<{name: string, current: boolean, remote: string}>>}
 */
export function refsBySHA(branches) {
  const rank = (b) => (b.current ? 0 : b.remote ? 2 : 1);
  const by = new Map();
  for (const b of branches || []) {
    if (!b.sha) continue;
    if (!by.has(b.sha)) by.set(b.sha, []);
    by.get(b.sha).push({ name: b.name, current: !!b.current, remote: b.remote || '' });
  }
  for (const [, list] of by) {
    list.sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  }
  return by;
}
