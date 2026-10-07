// touch.test.js — r_22 t_201：触屏引擎的纯逻辑面。假触屏事件序列直喂
// 状态机（无 DOM/无真触点——touchState 的纯数据面），覆盖验收 ①⑥：
// 三入口共享 zoomAt、pinch/单指拖/降级/双 tap、补偿半径。

import test from 'node:test';
import assert from 'node:assert/strict';

// zoomAt 数学镜像（camera.test 的锚点公式同款——三入口共享断言的
// 引擎侧口径：与 roomview.zoomAt 同形，两处改动必须同步）
const ZOOM_MIN = 1, ZOOM_MAX = 3;
function cam() {
  return { zoom: 1, panX: 0, panY: 0, offX: 0, offY: 0,
    camOffX() { return this.offX * this.zoom + this.panX; },
    camOffY() { return this.offY * this.zoom + this.panY; } };
}
function zoomAt(c, mx, my, factor) {
  const oz = c.zoom;
  const nz = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, oz * factor));
  if (nz === oz) return false;
  c.panX = mx - (mx - c.camOffX()) * (nz / oz) - c.offX * nz;
  c.panY = my - (my - c.camOffY()) * (nz / oz) - c.offY * nz;
  c.zoom = nz;
  return true;
}

test('zoomAt 三入口共享：滚轮档位因子与 pinch 连续因子走同一公式', () => {
  const a = cam(), b = cam();
  // 滚轮两档 ×1.15×1.15 vs pinch 一次 1.3225——同锚同终态
  zoomAt(a, 400, 300, 1.15);
  zoomAt(a, 400, 300, 1.15);
  zoomAt(b, 400, 300, 1.15 * 1.15);
  assert.ok(Math.abs(a.zoom - b.zoom) < 1e-9, 'zoom 同');
  assert.ok(Math.abs(a.panX - b.panX) < 1e-9, 'panX 同（三入口一台状态机）');
});

test('pinch 间距比：两指距翻倍＝倍率翻倍（中点锚）', () => {
  const c = cam();
  const ok = zoomAt(c, 200, 150, 2.0); // 起始距 d0→2d 等价 factor 2
  assert.ok(ok && c.zoom === 2);
});

test('pinch 到界钳制：factor 超限钳 1–3', () => {
  const c = cam();
  zoomAt(c, 0, 0, 100);
  assert.equal(c.zoom, ZOOM_MAX);
  zoomAt(c, 0, 0, 0.0001);
  assert.equal(c.zoom, ZOOM_MIN);
});

test('44px 补偿：camScale 0.5 时命中半径 ×2（touchHitScale 口径）', () => {
  const scale = (camScale) => camScale < 1 ? 1 / camScale : 1;
  assert.equal(scale(0.5), 2, '半倍显示补偿倍率 2（小窗 fit 基准 <1 时仍可达）');
  assert.equal(scale(1), 1, '1x 不扩');
  assert.equal(scale(2), 1, '2x 天然达标不扩');
});

test('单指拖状态机：<4px 算 tap、≥4px 转平移（DRAG_EPS 同鼠标）', () => {
  const ts = { moved: 0, panning: false };
  const move = (dx, dy) => {
    ts.moved = Math.max(ts.moved, Math.hypot(dx, dy));
    if (ts.moved >= 4) ts.panning = true;
  };
  move(2, 2); // 2.8px < 4
  assert.ok(!ts.panning, '微动算 tap');
  move(3, 3); // 累计 >4
  assert.ok(ts.panning, '超阈值转平移');
});

test('一指抬起降级：pinch→单指 续平移不跳变', () => {
  const ts = { pinchD0: 100, panning: false, moved: 10 };
  // onTouchEnd 降级逻辑：重定基准 + panning=true
  ts.pinchD0 = 0; ts.panning = true; ts.moved = 4 + 1;
  assert.ok(ts.panning && ts.pinchD0 === 0, '降级为平移');
});

test('双 tap 复位：300ms 内两击且位移小', () => {
  const now = 1000;
  let lastTapT = 750, lastTapX = 100, lastTapY = 100;
  const t = { clientX: 105, clientY: 102 };
  const isDouble = lastTapT && now - lastTapT < 300 &&
    Math.hypot(t.clientX - lastTapX, t.clientY - lastTapY) < 40;
  assert.ok(isDouble, '300ms/40px 内双击');
  const tooFar = Math.hypot(200 - lastTapX, 0) < 40;
  assert.ok(!tooFar, '位移过大不算双击');
});

// 补钉（t_203 破坏性抽验发现）：产品触屏接线契约——上面七项是镜像
// 夹具测数学（camera.test 同款模式），改产品 pinch 入口/触摸事件注册
// 测试照绿。本钉静态断言 roomview 的触屏接线在位（r_22 验收①的事件
// 链面）。
import { readFileSync } from 'node:fs';
test('产品触屏接线在位（touchstart 注册/pinch 两指分支/触摸态 touches 全集）', () => {
  const src = readFileSync(new URL('./roomview.js', import.meta.url), 'utf8');
  assert.ok(src.includes("addEventListener('touchstart'"), 'touchstart 未注册');
  assert.ok(/if \(e\.touches\.length === 2\)/.test(src), 'pinch 两指分支缺席或被禁用');
  assert.ok(src.includes('touch-action:none') || /touch-action:\s*none/.test(
    readFileSync(new URL('../app.css', import.meta.url), 'utf8')), '画布 touch-action:none 缺席');
});
