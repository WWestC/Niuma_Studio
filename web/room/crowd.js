// crowd.js — 人群分离（t_189 自 roomview 抽出为纯函数）：脚点贴得比
// SEP 近的两人互相推开，坐定的（working，工位/会议座/歇脚锚）是锚，
// 只推走动的一方。推出去的落点必须在可行走网格上——分轴试，两轴都
// 顶家具（贴桌沿/墙根的挤压形态）再试垂直方向，绝不把人推进桌子，
// 也不原地放弃留着两具身体穿模。纯运动学：输入 list+nav+bounds，
// 无 this、无渲染——collide.test 同款假时钟直接驱动。

import { clamp } from './util.js';

const SEP = 26;

/** 把 a 往 (mx,my) 推一步：落点出界夹回；顶家具分轴滑；两轴都堵
 *  再试垂直方向（推不开对方就把人从缝里剜出来）。轴试只在真位移时
 *  才算数——纯轴向推挤（uy≈0 的水平对峙）里 (a.x, a.y+my) 就是原
 *  地，原实现拿它当「y 轴可行」提前 return，一格都没挪（t_189 抓
 *  的轴对齐永不分离病理）。 */
export function nudgeBody(nav, bounds, a, mx, my) {
  const nx = clamp(a.x + mx, bounds.minX, bounds.maxX), ny = clamp(a.y + my, bounds.minY, bounds.maxY);
  if (nav && !nav.free(nx, ny)) {
    if (nx !== a.x && nav.free(nx, a.y)) { a.x = nx; return; }
    if (ny !== a.y && nav.free(a.x, ny)) { a.y = ny; return; }
    const len = Math.hypot(mx, my);
    if (len > 0.01) {
      const px = -my / len, py = mx / len;
      if (nav.free(a.x + px * 4, a.y + py * 4)) { a.x += px * 4; a.y += py * 4; return; }
      if (nav.free(a.x - px * 4, a.y - py * 4)) { a.x -= px * 4; a.y -= py * 4; return; }
    }
    return;
  }
  a.x = nx; a.y = ny;
}

/**
 * 人群分离一拍：全部两两检查，贴近就推开。pulseDone 是对子键集合
 * （t_162 分离脉冲：对向僵局的一次性垂直弹开，距离拉开自清）——
 * 调用方持有并在多帧间复用同一个 Set。
 */
export function separateCrowd(list, { nav, bounds, pulseDone }) {
  const SEP2 = SEP * SEP;
  for (let i = 0; i < list.length; i++) {
    for (let j = i + 1; j < list.length; j++) {
      const a = list[i], b = list[j];
      let dx = b.x - a.x, dy = b.y - a.y;
      let d2 = dx * dx + dy * dy;
      if (d2 >= SEP2) continue;
      let d = Math.sqrt(d2);
      if (d < 0.01) { dx = 1; dy = 0; d = 1; }
      const ux = dx / d, uy = dy / d, push = SEP - d;
      const aPinned = a.working, bPinned = b.working;
      if (!aPinned && !bPinned) {
        nudgeBody(nav, bounds, a, (-ux * push) / 2, (-uy * push) / 2);
        nudgeBody(nav, bounds, b, (ux * push) / 2, (uy * push) / 2);
        // t_162 分离脉冲：双方都走着且互相减速中（对向僵局的形态）——
        // 额外一次性 6px 垂直脉冲打破对称（两人朝相反侧弹开）。标记位
        // 防每帧重复；距离拉开后标记自清。
        const pk = a.name + '|' + b.name;
        const bothYielding = a.yieldHold > 0 || b.yieldHold > 0;
        if (bothYielding && d < 14 && pulseDone && !pulseDone.has(pk)) {
          pulseDone.add(pk);
          const s = a.name < b.name ? 1 : -1; // 确定性：名序定弹向
          nudgeBody(nav, bounds, a, -uy * 6 * s, ux * 6 * s);
          nudgeBody(nav, bounds, b, uy * 6 * s, -ux * 6 * s);
        }
        if (d > SEP) pulseDone?.delete(pk);
      } else if (!aPinned) nudgeBody(nav, bounds, a, -ux * push, -uy * push);
      else if (!bPinned) nudgeBody(nav, bounds, b, ux * push, uy * push);
    }
  }
}
