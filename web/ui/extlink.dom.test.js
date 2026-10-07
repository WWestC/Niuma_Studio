// extlink.dom.test.js — 外链跳默认浏览器（ui/extlink.js）的冒烟。
//
// 守门面：externalHref 与 markdown.js safeHref 同一放行口径——
// http(s)/mailto 绝对链接与站内绝对路径（补 origin）放行，#锚点、
// 空、相对引用、javascript:/data: 等一律不归管。处理器：桥在才拦
// （无桥＝浏览器，原生 target=_blank 自管），download 下载钮不碰，
// 拦下即 preventDefault＋桥收绝对 URL，桥同步炸/异步拒都不冒泡。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom } from '../room/testdom.js';
import { externalHref, extLinkHandler, installExtLinks } from './extlink.js';

const LOC = { href: 'http://127.0.0.1:8123/app' };

// node 世界没有全局 location（浏览器真身必有）——处理器的默认取址
// 靠它，测试世界直设一份（node <21 直赋、新版本可能 getter-only 强设）
const loc = { href: LOC.href };
try { globalThis.location = loc; } catch {
  Object.defineProperty(globalThis, 'location', { value: loc, configurable: true, writable: true });
}

// 事件/锚点用裸对象：处理器只吃 target.closest→getAttribute，真 DOM
// 的面（testdom 的 El.closest 恒 null）补不了交互，直给最小假件。
function anchor(href, attrs = {}) {
  const a = {
    getAttribute: (k) => (k === 'href' ? href : null),
    hasAttribute: (k) => k in attrs,
  };
  a.closest = () => a; // 真锚点：closest('a[href]') 摸到自己
  return a;
}
function clickEv(target) {
  let prevented = 0;
  return { ev: { target, preventDefault() { prevented++; } }, count: () => prevented };
}
function bridge(impl) {
  const calls = [];
  globalThis.openExternal = impl || ((u) => (calls.push(u), Promise.resolve()));
  return calls;
}
function noBridge() { delete globalThis.openExternal; }

test('externalHref 放行口径与 markdown safeHref 对齐', () => {
  assert.equal(externalHref('https://a.b/c?d=1', LOC), 'https://a.b/c?d=1');
  assert.equal(externalHref('http://a.b', LOC), 'http://a.b/');
  assert.equal(externalHref('mailto:a@b.c', LOC), 'mailto:a@b.c');
  // 站内绝对路径：补全 origin（附件图、kb 文档、编制表这些链接的壳面出口）
  assert.equal(externalHref('/media/x.png', LOC), 'http://127.0.0.1:8123/media/x.png');
  assert.equal(externalHref('/kb/docs/ops/establishment', LOC), 'http://127.0.0.1:8123/kb/docs/ops/establishment');
});

test('externalHref 拒管 #锚点/空/相对/危险 scheme', () => {
  assert.equal(externalHref('#top', LOC), null);
  assert.equal(externalHref('', LOC), null);
  assert.equal(externalHref('foo.html', LOC), null);
  assert.equal(externalHref('javascript:alert(1)', LOC), null);
  assert.equal(externalHref('data:text/html,<b>', LOC), null);
  assert.equal(externalHref('file:///etc/passwd', LOC), null);
});

test('处理器：桥在则拦下放行链接，preventDefault＋桥收绝对 URL', () => {
  const calls = bridge();
  const h = extLinkHandler();
  const e1 = clickEv(anchor('https://a.b/c'));
  h(e1.ev);
  assert.equal(e1.count(), 1);
  assert.deepEqual(calls, ['https://a.b/c']);
  const e2 = clickEv(anchor('/kb/docs/x'));
  h(e2.ev);
  assert.equal(e2.count(), 1);
  assert.deepEqual(calls, ['https://a.b/c', 'http://127.0.0.1:8123/kb/docs/x']);
  noBridge();
});

test('处理器：不归管的链接原样放行（不拦不调桥）', () => {
  const calls = bridge();
  const h = extLinkHandler();
  for (const href of ['#top', '', 'javascript:alert(1)', '/x']) {
    const e = clickEv(anchor(href, href === '/x' ? { download: '' } : undefined));
    h(e.ev);
    assert.equal(e.count(), 0, href);
  }
  assert.deepEqual(calls, []);
  noBridge();
});

test('处理器：无桥（普通浏览器）一根手指都不伸', () => {
  noBridge();
  const h = extLinkHandler();
  const e = clickEv(anchor('https://a.b/c'));
  h(e.ev);
  assert.equal(e.count(), 0); // 原生 target=_blank 接管
});

test('处理器：非链接目标与缺 closest 的目标不拦', () => {
  const calls = bridge();
  const h = extLinkHandler();
  const plain = clickEv({ closest: () => null });
  h(plain.ev);
  assert.equal(plain.count(), 0);
  const bare = clickEv({ getAttribute: () => 'https://a.b' }); // 文本节点类：无 closest
  h(bare.ev);
  assert.equal(bare.count(), 0);
  assert.deepEqual(calls, []);
  noBridge();
});

test('处理器：桥同步炸/异步拒都不冒泡', async () => {
  const boom = bridge(() => { throw new Error('bridge down'); });
  const h = extLinkHandler();
  const e = clickEv(anchor('https://a.b/c'));
  assert.doesNotThrow(() => h(e.ev));
  assert.equal(e.count(), 1);
  globalThis.openExternal = () => Promise.reject(new Error('denied'));
  assert.doesNotThrow(() => h(clickEv(anchor('https://a.b/c')).ev));
  await new Promise((r) => setTimeout(r, 0)); // 拒绝已吞，无 unhandledRejection
  noBridge();
});

test('installExtLinks 在 testdom 世界里装得上（document 路径不炸）', () => {
  dom();
  assert.doesNotThrow(() => installExtLinks());
});
