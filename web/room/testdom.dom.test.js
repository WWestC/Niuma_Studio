// testdom.dom.test.js — t_182（r_17）：基座自测——stub 自身行为断言。
// 基座是所有第一层冒烟的地基，它自己先得钉住：innerHTML 语义、
// fetch 契约五面（getJSON 真实消费）、localStorage 世界、清场干净。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from './testdom.js';

test('El：innerHTML/textContent 字符串槽＋appendChild/classList 无炸', () => {
  const el = new El('div');
  el.innerHTML = '<span>hi</span>';
  assert.equal(el.innerHTML, '<span>hi</span>');
  el.textContent = 'plain';
  assert.equal(el.textContent, 'plain');
  const child = el.appendChild(new El('span'));
  assert.equal(el.children.length, 1);
  assert.equal(child, el.children[0]);
  el.classList.add('x'); el.classList.toggle('y');
  assert.equal(typeof el.setAttribute, 'function');
});

test('dom()：document/world 立起，localStorage 可写读', () => {
  const { window } = dom();
  assert.ok(globalThis.document.createElement('div') instanceof El);
  assert.equal(window.location.host, 'localhost:7777');
  globalThis.localStorage.setItem('k', 'v');
  assert.equal(globalThis.localStorage.getItem('k'), 'v');
  assert.equal(globalThis.localStorage.getItem('nope'), null);
});

test('fetchStub：getJSON 契约五面（ok/status/headers.get/json）', async () => {
  dom();
  fetchStub([{ body: '2026-10-02｜交付｜小狐｜基座自测' }]);
  const resp = await globalThis.fetch('/x');
  assert.equal(resp.ok, true);
  assert.equal(resp.status, 200);
  assert.equal(resp.headers.get('Content-Type'), 'application/json');
  const data = await resp.json();
  assert.ok(String(data.body).includes('基座自测'));
});

test('fetchStub：数组按序耗尽重复末项＋error 形态（ok:false）', async () => {
  dom();
  fetchStub([{ json: { a: 1 } }, { ok: false, status: 500, json: { error: 'boom' } }]);
  const r1 = await globalThis.fetch(); // 第 1 项
  const r2 = await globalThis.fetch(); // 第 2 项（error 形态）
  const r3 = await globalThis.fetch(); // 耗尽重复末项
  assert.deepEqual(await r1.json(), { a: 1 });
  assert.equal(r2.ok, false);
  assert.equal(r2.status, 500);
  assert.equal(r3.status, 500);
});

test('resetDom：世界清干净（后续文件不受渗）', () => {
  dom();
  resetDom();
  assert.equal(globalThis.document, undefined);
  assert.equal(globalThis.fetch, undefined);
  assert.equal(globalThis.localStorage, undefined);
});

// 基座端到端预演：真被测模块（chronicle 面板）在 stub 世界里跑通
// ——这是 t_182 验收的「基座可用」活证（t_183 会展开成完整冒烟）。
test('端到端预演：ChroniclePanel 在 stub 世界渲染', async () => {
  dom();
  fetchStub([{ body: '2026-10-02｜交付｜小狐｜预演' }]);
  const { ChroniclePanel } = await import('../kb/chronicle.js');
  const p = new ChroniclePanel();
  const root = new El('div');
  await p.load(root);
  assert.equal(p.state, 'ready');
  assert.ok(root.innerHTML.includes('chron-list'), '时间线骨架');
  assert.ok(root.innerHTML.includes('预演'), '条目渲染');
  assert.ok(root.innerHTML.includes('chron-tp-delivery'), '类型标签');
  resetDom();
});
