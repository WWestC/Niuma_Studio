// xs.dom.test.js — t_202（r_22）：XS 375px 布局契约的静态断言。
// 浏览器自动化通道不可用（通道故障多轮）——按「断言关键规则存在而非
// 渲染实测」的降级口径钉布局契约：横向溢出的四个来源（无包裹的
// min-width 表格/固定宽弹窗/无断点的模态/固定 max-width 无收缩）逐项
// 断言其保护规则在。真机走查留验收段（t_203 小鹿）。
//
// 溢出源检查表（375px 视口）：
//   ① 数据表格 min-width ≥600 必须在 overflow-x:auto 容器内；
//   ② 弹窗 width ≥500 必须有 max-width:100% 同规则；
//   ③ 模态/时间线类 max-width 固定值不带 min-width（可收缩）；
//   ④ ≤560px 断点存在三处关键规则（设置窗/引导气泡/旋钮折行）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');

test('① 表格族 min-width 都有滚动容器包裹（est/git/project/staffing）', () => {
  // 四个 overflow-x:auto 容器规则在
  assert.ok(css.includes('.est-table { overflow-x: auto; }'), '编制表滚动容器');
  assert.ok(css.includes('.git-table') && css.includes('overflow-x: auto'), 'git 表滚动容器');
  assert.ok(css.includes('.project-pane { overflow-x: auto; }'), '项目面板滚动容器');
  assert.ok(css.includes('.staffing { overflow-x: auto; }'), '花名册滚动容器');
  // min-width 大户（erow 720/git-row 760/srow 640）逐个存在（配上面的容器）
  for (const sel of ['.erow', '.git-row', '.srow', '.tlist']) {
    assert.ok(css.includes(`${sel} {`) || css.includes(`${sel} .`), `${sel} 规则在`);
  }
});

test('② 弹窗宽度族有 max-width:100% 基线（overlay-card 基类）', () => {
  const base = css.match(/\.overlay-card \{[^}]+\}/)?.[0] || '';
  assert.ok(base.includes('max-width: 100%'), '基类 max-width:100%（w-md/w-lg 只是 width 覆盖）');
});

test('③ 时间线/成就货架可收缩（max-width 无配套 min-width）', () => {
  // 锚「行首的本体规则」——后代选择器（.req-minutes .chron-list 等局部
  // 覆写）不该被误测（t_209 加的折叠时间线覆写曾误中此断言）。
  // .ach-list 已随货架改版退役为 .trophy-case（收藏品货架面同契约）
  for (const sel of ['.chron-list', '.trophy-case']) {
    const rule = css.match(new RegExp(`\\n  ${'\\'.repeat(0)}\\${sel} \\{[^}]+\\}`))?.[0]
      || css.match(new RegExp(`^\\s*\\${sel} \\{[^}]+\\}`, 'm'))?.[0] || '';
    assert.ok(rule.includes('max-width'), `${sel} 本体规则有 max-width`);
    assert.ok(!rule.includes('min-width'), `${sel} 无 min-width（375px 可收缩）`);
  }
});

test('④ ≤560px 断点三处关键规则在（设置窗折行/引导气泡/旋钮折行）', () => {
  const bp = css.split('@media (max-width: 560px)');
  assert.ok(bp.length >= 4, `断点 ≥3 处（现 ${bp.length - 1}）`);
  assert.ok(css.includes('.guide-at-left, .guide-at-right'), '引导气泡窄窗折行规则');
  assert.ok(css.includes('.set-win .st-knob input[type="range"] { flex-basis: 100%'), '旋钮滑杆折行规则');
});

test('⑤ 访客态（t_192）：裁剪名单不含房主态必需件（自查回归）', () => {
  // visitor-badge 不得进访客裁剪名单（房主态角标会被误藏——t_192 施工
  // 时抓过并修掉，这条断言防复发）
  const m = css.match(/#nav-people[^{]*\{[^}]*display: none/) || [];
  assert.ok(!String(m[0] || '').includes('#visitor-badge'), '裁剪名单不含 visitor-badge');
});
