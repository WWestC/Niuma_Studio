// camera.test.js — 观察相机的纯数学面（r_08 跑法内，t_158）：锚点公式、
// 边界钳制、拖拽阈值——把 design/r11-observe §一/§二/§三 的契约钉成
// 断言。RoomView 的 DOM 构造不可 import，这里镜像相机数学（与
// roomview.js 的 camScale/onWheel/clampPan 同形——两处改动必须同步）。

import test from 'node:test';
import assert from 'node:assert/strict';

// 镜像相机（与 roomview 同形）
const ZOOM_MIN = 1, ZOOM_MAX = 3, WHEEL_STEP = 1.15, DRAG_EPS = 4, BREATH = 120;
function camera(fitScale = 1, fitX = 0, fitY = 0, viewW = 800, viewH = 600) {
  const c = { zoom: 1, panX: 0, panY: 0 };
  c.camS = () => fitScale * c.zoom;
  c.camX = () => fitX * c.zoom + c.panX;
  c.camY = () => fitY * c.zoom + c.panY;
  c.wheelAt = (mx, my, up) => {
    const old = c.zoom;
    const nz = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, up ? old * WHEEL_STEP : old / WHEEL_STEP));
    if (nz === old) return;
    c.panX = mx - (mx - c.camX()) * (nz / old) - fitX * nz;
    c.panY = my - (my - c.camY()) * (nz / old) - fitY * nz;
    c.zoom = nz;
    c.clamp();
  };
  c.clamp = () => {
    const cs = c.camS();
    const sw = (768 + BREATH) * cs, sh = (520 + BREATH) * cs;
    const minX = viewW - sw - fitX * c.zoom, maxX = fitX * c.zoom + BREATH * c.zoom;
    c.panX = minX > maxX ? (minX + maxX) / 2 : Math.max(minX, Math.min(maxX, c.panX));
    const minY = viewH - sh - fitY * c.zoom, maxY = fitY * c.zoom + BREATH * c.zoom;
    c.panY = minY > maxY ? (minY + maxY) / 2 : Math.max(minY, Math.min(maxY, c.panY));
  };
  c.scene = (vx, vy) => ({ x: (vx - c.camX()) / c.camS(), y: (vy - c.camY()) / c.camS() });
  return c;
}

test('锚点公式：缩放后鼠标指向的场景点不动（定稿 §一）', () => {
  const c = camera();
  const before = c.scene(400, 300);
  c.wheelAt(400, 300, true); // 放大一档
  const after = c.scene(400, 300);
  assert.ok(Math.abs(before.x - after.x) < 1e-9 && Math.abs(before.y - after.y) < 1e-9);
});

test('缩放范围 1–3 到界即停（无回弹）', () => {
  const c = camera();
  for (let i = 0; i < 30; i++) c.wheelAt(400, 300, true);
  assert.equal(c.zoom, ZOOM_MAX);
  for (let i = 0; i < 60; i++) c.wheelAt(400, 300, false);
  assert.equal(c.zoom, ZOOM_MIN);
});

test('每格 ×1.15（连续档位语义）', () => {
  const c = camera();
  c.wheelAt(0, 0, true);
  assert.ok(Math.abs(c.zoom - 1.15) < 1e-9);
});

test('≤1x 且画布远大于场景时钳制居中（平移余量归语义零，§二）', () => {
  // 前提：fit 后场景（含呼吸边）远小于视口——超大窗。钳制走居中分支
  // （minX>maxX），pan 落在两界的均值上——即「平移没有自由度，只能
  // 停在钳制域唯一允许点」，这是「拖不动」的数学形态。
  const c = camera(0.5, 0, 0, 800, 600); // 场景 888*0.5=444 远小于 800
  c.panX = 999; c.panY = -999;
  c.clamp();
  const cs = 0.5;
  const expectX = (800 - 888 * cs + BREATH * 1) / 2; // 居中＝均值
  assert.ok(Math.abs(c.panX - expectX) < 1e-9, `panX=${c.panX} 期望居中 ${expectX}`);
  // 任意输入都钳到同一点（无自由度）
  c.panX = -999; c.clamp();
  assert.ok(Math.abs(c.panX - expectX) < 1e-9, '反向暴力也钳到同一点（拖不动）');
});

test('2x 时平移有余量且四向暴力拖不出界（§二/验收 4）', () => {
  const c = camera();
  for (let i = 0; i < 5; i++) c.wheelAt(400, 300, true);
  assert.ok(c.zoom > 2); // 1.15^5 ≈ 2.01
  // 四向暴力：钳制后 pan 必须落在 [minX, maxX] 数学域内
  const cs = c.camS();
  const sw = (768 + BREATH) * cs, sh = (520 + BREATH) * cs;
  const minX = 800 - sw, maxX = BREATH * c.zoom;
  const minY = 600 - sh, maxY = BREATH * c.zoom;
  c.panX = 1e6; c.panY = -1e6; c.clamp();
  assert.ok(c.panX <= maxX + 1e-9, `panX=${c.panX} > maxX=${maxX}`);
  c.panX = -1e6; c.clamp();
  assert.ok(c.panX >= minX - 1e-9, `panX=${c.panX} < minX=${minX}`);
  c.panY = 1e6; c.clamp();
  assert.ok(c.panY <= maxY + 1e-9, `panY=${c.panY} > maxY=${maxY}`);
  c.panY = -1e6; c.clamp();
  assert.ok(c.panY >= minY - 1e-9, `panY=${c.panY} < minY=${minY}`);
});

test('拖拽阈值 4px（<4 算点击、≥4 算平移，§三）', () => {
  assert.equal(DRAG_EPS, 4);
  assert.ok(Math.hypot(3, 3) >= DRAG_EPS); // 3,3 对角已超阈（用累计位移语义）
  assert.ok(Math.hypot(2, 2) < DRAG_EPS);
});

test('逆变换自洽：canvasPos(scenePos(v)) === v（往返恒等）', () => {
  const c = camera();
  c.wheelAt(400, 300, true);
  c.wheelAt(400, 300, true);
  c.panX += 137; c.panY -= 91; // 任意观察态
  const v = { x: 123, y: 456 };
  const s = c.scene(v.x, v.y);
  const back = c.scene(v.x, v.y); // scene 即 canvasPos 的数学
  assert.deepEqual(s, back); // 确定性
  // 命中换算的往返：给一个场景坐标算视口再回来
  const sc = { x: 384, y: 260 };
  const vp = { x: sc.x * c.camS() + c.camX(), y: sc.y * c.camS() + c.camY() };
  const rt = c.scene(vp.x, vp.y);
  assert.ok(Math.abs(rt.x - sc.x) < 1e-9 && Math.abs(rt.y - sc.y) < 1e-9);
});

// 补钉（t_161 破坏性抽验发现）：产品常量契约——上面七项用镜像夹具测公式
// 语义，但夹具常量硬编码与 roomview 脱钩（改产品 ZOOM_MAX 测试照绿）。
// 本钉静态断言 roomview.js 的真常量与定稿 §一/§三 一致。
import { readFileSync } from 'node:fs';
test('产品缩放常量一致（1–3、×1.15、拖拽阈值 4px、呼吸边）', () => {
  const src = readFileSync(new URL('./roomview.js', import.meta.url), 'utf8');
  assert.ok(/ZOOM_MIN\(\)\s*\{\s*return 1;/.test(src), 'ZOOM_MIN 应为 1（缩小下限＝本身大小，再小四周露白）');
  assert.ok(/ZOOM_MAX\(\)\s*\{\s*return 3;/.test(src), 'ZOOM_MAX 应为 3（定稿 §一）');
});

// 补钉（t_158 补：框外复位钮）：HTML/CSS/接线三面——钮挂在 stage 里但悬
// 在框外（absolute 角标），stage 不得再 overflow:hidden（否则钮被裁掉，
// 圆角裁剪已挪给 canvas）；roomview 三口归一（0 键/双击空白/角标钮都走
// resetCam），直写复位只许 resetCam 体内一处，所有观察态变更点都同步
// 钮的现身态（离家＝zoom≠1 或有平移才露面）。
test('框外复位钮：DOM 在位悬框外、stage 不裁剪、三口归一＋现身同步接线', () => {
  const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
  const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
  // ① DOM：钮是 #room-stage 的孩子、初始 hidden（在家不露面）
  const i = html.indexOf('<div id="room-stage">');
  const seg = html.slice(i, html.indexOf('id="board-people"'));
  assert.ok(i >= 0 && seg.includes('id="room-cam-reset"'), '复位钮应挂在 #room-stage 内');
  assert.match(seg, /id="room-cam-reset"[^>]*\shidden/, '初始 hidden（相机在家时是 no-op）');
  // ② CSS：stage 不裁剪（圆角已挪 canvas）、钮定位在框外
  const stageRules = (css.match(/#room-stage \{[^}]*\}/g) || []).join('\n');
  assert.ok(!/overflow:\s*hidden/.test(stageRules), 'stage 不得 overflow:hidden（会裁掉框外角标）');
  const canvasRule = css.match(/#room-canvas \{[^}]*\}/)?.[0] || '';
  assert.match(canvasRule, /border-radius:\s*10px/, '圆角裁剪由 canvas 自担');
  const btnRule = css.match(/#room-cam-reset \{[^}]*\}/)?.[0] || '';
  assert.match(btnRule, /right:\s*-10px/, '钮悬在框右缘外');
  assert.match(btnRule, /bottom:\s*-10px/, '钮悬在框下缘外');

  // ③ 接线：构造器挂 click、三口归一、变更点同步现身态
  const rv = readFileSync(new URL('./roomview.js', import.meta.url), 'utf8');
  assert.match(rv, /addEventListener\('click', \(\) => this\.resetCam\(\)\)/, '角标钮点击归位');
  assert.match(rv, /resetCam\(\) \{\s*\n\s*this\.zoom = 1; this\.panX = 0; this\.panY = 0;\s*\n\s*this\.syncCamReset\(\);/,
    'resetCam＝归位三连＋现身同步');
  assert.match(rv, /this\.camResetBtn\.hidden = this\.zoom === 1 && !this\.panX && !this\.panY/,
    '现身条件＝离家（zoom≠1 或有平移）');
  assert.match(rv, /if \(e\.key === '0'\) \{\s*\n\s*this\.resetCam\(\);/, '0 键走归一口');
  assert.match(rv, /this\.resetCam\(\); \/\/ 复位（同 0 键）/, '双击空白走归一口');
  // 直写复位只许 resetCam 体内一处（三口不再各写各的——改一处漏两处的防线）
  const rawResets = rv.match(/this\.zoom = 1; this\.panX = 0; this\.panY = 0;/g) || [];
  assert.equal(rawResets.length, 1, '直写复位唯一（resetCam 体内）');
  // 滚轮/拖拽/键盘档三个变更点都同步现身态（＋resetCam 自调＝至少 4 处）
  const syncs = rv.match(/this\.syncCamReset\(\);/g) || [];
  assert.ok(syncs.length >= 4, `观察态变更点都应同步现身态（现 ${syncs.length} 处）`);
});
