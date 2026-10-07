// gitsettings.dom.test.js — 设置中心「项目」页（两级门顶层的第三个入
// 口）的 DOM 冒烟：页签与双开关在；随当前房回读渲染（enabled=false →
// 总开关不勾、自动建枝置灰、行内小签带房名）；没有当前房时置灰。点
// 击路由不进第一层冒烟（testdom 家法）。
//
//   node --test web/ui/gitsettings.dom.test.js

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from '../room/testdom.js';

// 记忆补丁（projectnew.dom.test.js 同款）：stub 的 querySelector 每次
// 回新节点，回读后置属性需要按选择器持久化。
const origQS = El.prototype.querySelector;
const nodeCache = new WeakMap();
El.prototype.querySelector = function (sel) {
  if (!nodeCache.has(this)) nodeCache.set(this, {});
  const c = nodeCache.get(this);
  if (!(sel in c)) c[sel] = origQS.call(this, sel);
  return c[sel];
};

async function bootSettings(summary, project) {
  dom();
  globalThis.localStorage.setItem('dh.ui.prefs', JSON.stringify({ lang: 'zh' }));
  globalThis.location = { origin: 'http://127.0.0.1:7420' }; // openFor 读 location.origin（嵌入形态行）
  const { SettingsWin } = await import('./settings.js');
  fetchStub(summary); // 恒回：各 #load* 面都吃同一份也无妨
  const win = new SettingsWin({ project });
  win.openFor(null);
  await new Promise((r) => setTimeout(r, 20)); // 等异步回读落账
  return win;
}

test('「项目」页：git 四行随当前房回读渲染', async () => {
  const win = await bootSettings(
    { enabled: false, base_branch: 'dev', auto_seat_branch: true, auto_seat: true },
    () => ({ key: 'book', name: '小说编写' }));
  const html = globalThis.document.body.children.at(-1)._html;
  assert.ok(html.includes('data-sw-pane="proj"'), '「项目」页签在');
  assert.ok(html.includes('data-st-git') && html.includes('data-st-gitauto') && html.includes('data-st-gitseat'), '三个开关在');
  assert.ok(html.includes('data-st-gitbase') && html.includes('data-st-gitbaseset'), '基线分支行在');
  const gitBtn = win.el.querySelector('[data-st-git]');
  const autoBtn = win.el.querySelector('[data-st-gitauto]');
  const seatBtn = win.el.querySelector('[data-st-gitseat]');
  const baseIn = win.el.querySelector('[data-st-gitbase]');
  const nameEl = win.el.querySelector('[data-st-gitname]');
  assert.equal(gitBtn.getAttribute('aria-checked'), 'false', 'enabled=false → 总开关不勾');
  assert.equal(autoBtn.disabled, true, '总开关关着 → 自动建枝置灰');
  assert.equal(autoBtn.getAttribute('aria-checked'), 'true', 'auto_seat_branch=true 回显');
  assert.equal(seatBtn.disabled, true, '总开关关着 → 自动驻枝置灰');
  assert.equal(seatBtn.getAttribute('aria-checked'), 'true', 'auto_seat=true 回显');
  assert.equal(baseIn.value, 'dev', '基线分支回显生效值');
  assert.equal(nameEl.textContent, '小说编写', '行内小签带当前房名');
  win.close();
  resetDom();
});

test('「项目」页：没有当前房时四行置灰指路', async () => {
  const win = await bootSettings({ enabled: true }, () => null);
  const gitBtn = win.el.querySelector('[data-st-git]');
  const autoBtn = win.el.querySelector('[data-st-gitauto]');
  const seatBtn = win.el.querySelector('[data-st-gitseat]');
  const baseIn = win.el.querySelector('[data-st-gitbase]');
  const nameEl = win.el.querySelector('[data-st-gitname]');
  assert.equal(gitBtn.disabled, true, '无当前房 → 总开关置灰');
  assert.equal(autoBtn.disabled, true, '无当前房 → 自动建枝置灰');
  assert.equal(seatBtn.disabled, true, '无当前房 → 自动驻枝置灰');
  assert.equal(baseIn.disabled, true, '无当前房 → 基线输入置灰');
  assert.equal(nameEl.textContent, '暂无所在房', '小签如实');
  win.close();
  resetDom();
});
