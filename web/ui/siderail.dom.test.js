// siderail.dom.test.js — 侧栏收起态图标轨的布局契约（静态断言，xs.dom.test.js
// 同款降级口径）。收起态的坑：#side > section 在收起时仍定宽 240px（hover
// 浮展开免重排），行若跟着占满 220px，justify-content:center 是在 220px 里
// 居中——图标全被送到 x≈90-110，落在 56px 轨外被 overflow 裁掉，整条轨只剩
// 药丸底和半截「项目空间」（v2.12 实测事故）。契约：行宽必须收成 36px
// （56 − section 左右内边距 2×10），居中才发生在可视轨内；S 档半收起块与
// M+ 折叠块同构，规则必须成对在。
//
// 浏览器自动化通道不可用时的降级口径：断言关键规则存在而非渲染实测；
// 真机走查（图标轨居中/hover 浮展开/徽标钉位）留验收。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');

// S 档半收起块（常态图标栏）与 M+ 折叠块的同一条件选择器前缀
const S = '#side:not(:hover):not(:focus-within)';
const M = `html[data-side-collapsed] ${S}`;

const escRe = (s) => s.replaceAll('.', '\\.').replaceAll('[', '\\[').replaceAll(']', '\\]')
  .replaceAll('(', '\\(').replaceAll(')', '\\)');

for (const [label, pre] of [['S 档', S], ['M+ 折叠', M]]) {
  test(`${label}：行宽收成 36px（居中发生在 56px 可视轨内）`, () => {
    // 56px 轨与 240px 定宽 section 都在（36px 的推导前提）
    assert.ok(css.includes('width: 56px; overflow: hidden;'), `${label} 轨宽 56 + overflow 裁切`);
    assert.ok(css.includes('#side > section { width: 240px; }'), `${label} section 定宽 240`);
    // 板块链接与房间/成员/房主行：36px 格
    assert.match(css, new RegExp(`${escRe(pre)} nav a \\{[^}]*width: 36px`));
    assert.match(css, new RegExp(`${escRe(pre)} :is\\(\\.room-row, \\.member, #owner-card\\) \\{[^}]*width: 36px`));
  });

  test(`${label}：项目空间入口收成纯图标（▾ 居中可点，不露半截文字）`, () => {
    assert.match(css, new RegExp(`${escRe(pre)} #side-space \\{[^}]*width: 36px`));
    assert.ok(css.includes(`${pre} #side-space > span { display: none; }`), `${label} 入口文字列收起`);
  });

  test(`${label}：行悬停 affordance 与身份底排的收起态`, () => {
    assert.match(css, new RegExp(`${escRe(pre)} #ident-row \\{[^}]*flex-direction: column`), `${label} 身份底排竖排`);
    assert.ok(css.includes(`${pre} .room-pause { display: none; }`), `${label} 暂停钮收起（浮展开再回来）`);
  });

  test(`${label}：干活中/待投 chip 收起（不把头像挤出 36px 格）`, () => {
    assert.match(css, new RegExp(`${escRe(pre)} :is\\(\\.work-chip, \\.wait-chip\\) \\{ display: none`));
  });

  test(`${label}：标题空位不留洞（display:none 而非 visibility）`, () => {
    // 板块/在线牛马标题若 visibility:hidden，空位照占——56px 轨每段开头一个 ~28px 的洞
    assert.ok(css.includes(`${pre} h2 { display: none; }`), `${label} 标题 display:none`);
    assert.ok(css.includes(`${pre} #member-head-row { display: none; }`), `${label} 标题行整行收起`);
    const head = css.indexOf(`${pre} h2`);
    assert.ok(!/visibility:\s*hidden/.test(css.slice(head, head + 80)), `${label} 不再走 visibility`);
  });
}
