// usage.test.js — 耗粮统计抽屉的行为面：只测纯逻辑（fmtTokens 的人话
// 分档 / fmtDur 的时长分档 / 回合状态中文面），再按 sfx.test.js 的接线
// 扫描先例钉源码契约——侧栏钮、api 封装、路由挂载、sheet 样式四处接
// 线任一处被删，这里必红（面板哑火不是「没数据」是「没接线」）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fmtTokens, fmtDur, turnStatusText, fmtBucketLabel, lineGeometry } from './usageview.js';

// 中文契约面：fmtTokens/fmtDur/turnStatusText 已是语言敏感输出
//（ui/i18n.js——en 走 K/M/B 与 done/running），裸 Node 的
// navigator.language 是 en-US，先钉 zh 偏好再断言中文形（en 面在
// ui/i18n.test.js 另钉）。t() 在调用时才读偏好，import 后立桩即可。
try {
  globalThis.localStorage = {
    _m: new Map([['dh.ui.prefs', JSON.stringify({ lang: 'zh' })]]),
    getItem(k) { return this._m.has(k) ? this._m.get(k) : null; },
    setItem(k, v) { this._m.set(k, String(v)); },
  };
} catch { /* 已有可写 localStorage 的环境照用 */ }

// ── ① fmtTokens：token 数的人话分档 ──────────────────────────────

test('fmtTokens 亿两位小数 / 万一位小数 / 万以下原样', () => {
  assert.equal(fmtTokens(474790418), '4.75 亿');
  assert.equal(fmtTokens(100000000), '1.00 亿');
  assert.equal(fmtTokens(474428279 - 472984832), '144.3 万'); // 1443447 → 144.3447 万
  assert.equal(fmtTokens(10000), '1.0 万');
  assert.equal(fmtTokens(9999), '9999');
  assert.equal(fmtTokens(0), '0');
  assert.equal(fmtTokens(-1), '-1'); // 台账不该出负数，出了也别炸
  assert.equal(fmtTokens(NaN), '—');
  assert.equal(fmtTokens(undefined), '—');
});

// ── ② fmtDur：时长的人话分档（null/0 = 「—」）───────────────────

test('fmtDur 秒（<10 一位小数）→ 分秒 → 时分；缺席是「—」', () => {
  assert.equal(fmtDur(24121), '24 秒');
  assert.equal(fmtDur(8500), '8.5 秒');
  assert.equal(fmtDur(59248), '59 秒');
  assert.equal(fmtDur(60000), '1 分 0 秒');
  assert.equal(fmtDur(3545000), '59 分 5 秒'); // 分秒段的门槛内侧
  assert.equal(fmtDur(3600000), '1 时 00 分');
  assert.equal(fmtDur(3720000 + 120000), '1 时 04 分'); // 64 分＝1 时 04 分
  assert.equal(fmtDur(0), '—');
  assert.equal(fmtDur(null), '—');
  assert.equal(fmtDur(undefined), '—');
});

// ── ③ 回合状态中文面 ─────────────────────────────────────────────

test('turnStatusText 四态中文、生面孔透出、缺席是「—」', () => {
  assert.equal(turnStatusText('completed'), '完成');
  assert.equal(turnStatusText('running'), '进行中');
  assert.equal(turnStatusText('error'), '出错');
  assert.equal(turnStatusText('cancelled'), '已取消');
  assert.equal(turnStatusText('weird'), 'weird');
  assert.equal(turnStatusText(''), '—');
  assert.equal(turnStatusText(undefined), '—');
});

// ── ④ 接线扫描（删闸必红）───────────────────────────────────────

const read = (p) => readFileSync(new URL(p, import.meta.url), 'utf8');

test('四处接线在位：侧栏钮 / app 装配 / api 封装 / 服务端路由与样式', () => {
  const html = read('../index.html');
  const css = read('../app.css');
  assert.match(html, /id="side-usage"/, 'index.html 侧栏耗粮钮');
  assert.match(css, /\.uv-sheet \{ width: 680px; \}/, '抽屉样式在位');

  const app = read('../app.js');
  assert.match(app, /import \{ UsagePanel \} from '\.\/ui\/usageview\.js';/, 'app.js 引入 UsagePanel');
  assert.match(app, /usagePanel = new UsagePanel\(/, 'app.js 实例化');
  assert.match(app, /\$\('#side-usage'\)\.addEventListener\('click', \(\) => usagePanel\.open\(\)\)/, '侧栏钮点开');

  const api = read('../wire/api.js');
  assert.match(api, /export function getUsage\(\)/, 'api.js 总览封装');
  assert.match(api, /export function getUsageTurns\(/, 'api.js 明细封装');

  const ws = read('../../server/ws.go');
  // 路由自 routeMatrix 收敛后走 s.route（角色矩阵门），钉新形。
  assert.match(ws, /s\.route\(mux, "\/usage", s\.handleUsage\)/, '路由挂载 /usage');
  assert.match(ws, /s\.route\(mux, "\/usage\/turns", s\.handleUsageTurns\)/, '路由挂载 /usage/turns');
});

test('员工列表头居中＋名称悬浮出全名（截断兜底）', () => {
  const css = read('../app.css');
  assert.match(css, /\.uv-row-head span:first-child \{ text-align: center; \}/, '表头「员工」居中规则');

  const view = read('./usageview.js');
  assert.match(view, /class="uv-name" title="\$\{esc\(e\.title\)\}"/, '名称 span 带全名 title（悬停显示、移开消失）');
});

// ── ⑤ 趋势槽（/usage 回包里的 series 折线＋模型对比）────────────

test('fmtBucketLabel 时/天/周/月四形（本地时区、补零）', () => {
  assert.equal(fmtBucketLabel(new Date(2026, 0, 5, 14).getTime(), 'hour'), '01-05 14:00');
  assert.equal(fmtBucketLabel(new Date(2026, 0, 5, 9, 30).getTime(), 'hour'), '01-05 09:00');
  assert.equal(fmtBucketLabel(new Date(2026, 0, 5, 23, 59).getTime(), 'day'), '01-05');
  assert.equal(fmtBucketLabel(new Date(2026, 0, 5).getTime(), 'week'), '01-05');
  assert.equal(fmtBucketLabel(new Date(2026, 2, 9).getTime(), 'month'), '2026-03');
});

test('lineGeometry 峰值整桶 / 网格三线 / 坐标自左向右 / 全零与单点不炸', () => {
  const g = lineGeometry([0, 250, 100], 640, 200, 10, 10, 20, 24);
  assert.equal(g.yMax, 250); // 250 吸到 2.5×10² 的整桶
  assert.deepEqual(g.grid.map((r) => r.v), [250, 125, 0]);
  assert.ok(g.pts[0][0] < g.pts[1][0] && g.pts[1][0] < g.pts[2][0], '旧→新自左向右');
  assert.ok(Math.abs(g.pts[1][1] - 20) < 1e-9, '峰值顶到 padT');
  assert.ok(Math.abs(g.pts[0][1] - g.base) < 1e-9, '零值贴零线');

  // 峰值取整档：1/2/2.5/5×10^k
  assert.equal(lineGeometry([3], 100, 100, 0, 0, 0, 0).yMax, 5);
  assert.equal(lineGeometry([8000000], 100, 100, 0, 0, 0, 0).yMax, 10000000);

  // 全零给 yMax=1 不除零（平线）；单点居中；空列只剩网格。
  const z = lineGeometry([0, 0], 100, 100, 0, 0, 0, 0);
  assert.equal(z.yMax, 1);
  assert.equal(z.pts[0][1], z.pts[1][1]);
  const one = lineGeometry([5], 100, 100, 10, 10, 0, 0);
  assert.ok(Math.abs(one.pts[0][0] - 50) < 1e-9);
  assert.deepEqual(lineGeometry([], 100, 100, 0, 0, 0, 0).pts, []);
});

test('趋势槽接线在位：series 槽 / 四档页签 / 悬停件 / 趋势样式', () => {
  const view = read('./usageview.js');
  assert.match(view, /import \{ getUsage, getUsageTurns \} from '\.\.\/wire\/api\.js';/, 'usageview 引总览封装（series 同包）');
  assert.match(view, /data\.series/, '趋势面吃总览回包（一趟扫账无第二读）');
  assert.match(view, /data-uv-charts/, '总览页趋势槽');
  assert.match(view, /data-uv-range/, '四档页签');
  assert.match(view, /uv-guide/, '悬停纵导线');
  assert.match(view, /uv-tip/, '悬停浮牌');

  const api = read('../wire/api.js');
  assert.match(api, /export function getUsage\(\)/, 'api.js 总览封装');
  assert.match(api, /series: import\('\.\.\/ui\/usageview\.js'\)\.UsageSeriesReport/, 'api.js 文档带 series 形状');
  assert.doesNotMatch(api, /usage\/series/, '独立的 /usage/series 端点已并入 /usage');

  const css = read('../app.css');
  assert.match(css, /\.uv-line \{ position: relative; \}/, '趋势图容器样式');
  assert.match(css, /\.uv-mrow \{/, '模型对比条样式');
});
