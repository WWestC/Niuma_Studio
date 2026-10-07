// util.js — the room board's small shared helpers (clamp/rand over the
// sim, rect tests and pixel-art fill primitives over the painters).
// Nothing here knows about actors or art assets.

export const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
export const rand = (a, b) => a + Math.random() * (b - a);

/** Point-in-rect against an {x,y,w,h} (or null → false). */
export function inRect(x, y, r) {
  return !!r && x >= r.x && x < r.x + r.w && y >= r.y && y < r.y + r.h;
}

// roundedFillL: the union of a horizontal and a vertical band — the
// pixel-art "rounded" corner cut, k px.
export function rounded(ctx, x, y, w, h, k, style) {
  ctx.fillStyle = style;
  ctx.fillRect(x + k, y, w - 2 * k, h);
  ctx.fillRect(x, y + k, w, h - 2 * k);
}

/** '#rrggbb' → an rgba() string with a fade multiplier and alpha. */
export function withAlpha(hex, f, a = 1) {
  const n = parseInt(hex.slice(1), 16);
  const r = (n >> 16) & 255, g = (n >> 8) & 255, b = n & 255;
  return `rgba(${r},${g},${b},${f * a})`;
}

/** 首次引导的放行判式（t_159 修复）：视图可见、房间数据已装
 *  （roomKey）、启动门已落（doorDown），且本次会话未弹过——缺一门
 *  都不放行。房主实录：路由首拍跑在 setRoom 与开门之前，气泡画在
 *  黑屏/启动门上（guide-layer z-120 还压过门 z-90）。 */
export function guideDue(st) {
  return !st.guideShown && !!st.visible && !!st.roomKey && !!st.doorDown;
}
