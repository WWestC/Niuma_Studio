// kbdiff.test.js — 黑板写播报卡红绿对比的模板钉：kb 广播帧的 diff 行
// （'-' 删 '+' 增）画成 .kb-diff 行（del/add 二色），文档行定位哪篇
// （题名/key/rev），超帽省略数落 .kb-diff-more，正文一律转义（diff 文本
// 是牛马写进黑板的原话，不能带 HTML 进气泡）；无 diff 帧 → 空串（调用
// 方退回旧的事件词渲染，denied/旧服务端不受影响）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { kbDiffHTML } from './kbdiff.js';
// zh 钉子：裸 Node 的 navigator.language 是 en-US，本文件断言的中文文案
// 已全线走 t()（i18n.js）——先钉 zh 偏好再断言（usage.test.js 同款；
// en 面归 ui/i18n.test.js）。t() 调用时才读偏好，import 后立桩即可。
try {
  globalThis.localStorage = {
    _m: new Map([['dh.ui.prefs', JSON.stringify({ lang: 'zh' })]]),
    getItem(k) { return this._m.has(k) ? this._m.get(k) : null; },
    setItem(k, v) { this._m.set(k, String(v)); },
  };
} catch { /* 已有可写 localStorage 的环境照用 */ }


test('增删行成对出 del/add 二色行，± 记号与文本分列', () => {
  const html = kbDiffHTML({ diff: [
    { k: '-', t: '旧句子' },
    { k: '+', t: '新句子' },
  ] });
  assert.match(html, /<div class="kb-diff-row del">/);
  assert.match(html, /<div class="kb-diff-row add">/);
  assert.match(html, /<span class="kd-sign">-<\/span><span class="kd-text">旧句子<\/span>/);
  assert.match(html, /<span class="kd-sign">\+<\/span><span class="kd-text">新句子<\/span>/);
});

test('文档行：题名＋key（等宽）＋rev 定位是哪篇', () => {
  const html = kbDiffHTML({
    diff: [{ k: '+', t: '一行' }],
    doc: { title: 'HR 工作手册', key: 'roles/hr', rev: 3 },
  });
  assert.match(html, /「HR 工作手册」/);
  assert.match(html, /<span class="kd-key">roles\/hr<\/span>/);
  assert.match(html, /rev 3/);
});

test('文档行去重：题名与 key 同文只念一遍（key 留下）', () => {
  const html = kbDiffHTML({
    diff: [{ k: '+', t: '一行' }],
    doc: { title: 'design/r31-parking', key: 'design/r31-parking', rev: 1 },
  });
  assert.ok(!html.includes('「'), '同文题名不得再「」一遍');
  assert.match(html, /<span class="kd-key">design\/r31-parking<\/span>/);
  assert.match(html, /rev 1/);
});

test('正文转义：黑板原话里的 HTML 永不裸进气泡', () => {
  const html = kbDiffHTML({ diff: [{ k: '+', t: '<img src=x onerror=alert(1)>“&”' }] });
  assert.ok(!html.includes('<img'), 'img 标签必须被转义');
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
  assert.match(html, /&amp;/);
});

test('超帽省略数落 .kb-diff-more；行宽纵深夹断带省略号', () => {
  const html = kbDiffHTML({ diff: [{ k: '+', t: '短行' }], diff_more: 42 });
  assert.match(html, /<div class="kb-diff-more">…另有 42 行未展示<\/div>/);
  const long = kbDiffHTML({ diff: [{ k: '-', t: '牛'.repeat(300) }] });
  assert.match(long, /…<\/span>/);
  assert.ok(!long.includes('牛'.repeat(201)), '超宽行必须截到 200 字＋省略号');
});

test('无 diff 帧返回空串（denied/旧服务端/内容未变退回旧渲染）', () => {
  assert.equal(kbDiffHTML({}), '');
  assert.equal(kbDiffHTML({ diff: [] }), '');
  assert.equal(kbDiffHTML({ diff: null, text: '文档 ops/x 不存在' }), '');
});

// stream.js 无 DOM 测试基建（interrupted.test.js 先例）：接线只能源扫描——
// Msg.Kb 分支必须真的把 kbDiffHTML 挂进 #noticeRow 的 body（fold.js 后
// 包一层折叠区——整篇定稿的写入动辄几十行，长清单自动收起），且无
// diff 时落回旧的事件词渲染；任一处被删，卡就退回孤零零一个「写入/追加」。
// 「看全文」是 diff 百行帽（diff_more）的逃生口：doc.key 在场必挂钮，
// 点击面经 opts.openDoc 出站（app.js 拉 /kb/docs 落全文面板）。
test('接线：#noticeRow 的 kb 分支挂 kbDiffHTML（带折叠区），空 diff 落回旧渲染', () => {
  const src = readFileSync(new URL('./stream.js', import.meta.url), 'utf8');
  assert.match(src, /import \{ kbDiffHTML \} from '\.\/kbdiff\.js';/, '须引入 kbdiff 模块');
  const branch = src.match(/frame\.type === Msg\.Kb && \(frame\.diff \|\| \[\]\)\.length[\s\S]*?body = foldZoneHTML\(`<div class="nc-body">/);
  assert.ok(branch, 'kb＋有 diff 分支须存在，且其后仍有 nc-body 旧渲染兜底');
  assert.match(branch[0], /body = foldZoneHTML\(kbDiffHTML\(frame\),/, '有 diff 的黑板帧正文画红绿卡（住折叠区），看全文钮作区脚尾件与展开同排');
  assert.match(branch[0], /data-kbfull/, 'diff 卡带看全文钮（doc.key 在场才出）');
  const click = src.match(/const kbfull = t\.closest\('\[data-kbfull\]'\);[\s\S]*?return;\n    \}/);
  assert.ok(click, '点击面须有 data-kbfull 分支');
  assert.match(click[0], /this\.opts\.openDoc\?\.\(kbfull\.dataset\.kbfull\)/, '看全文经 opts.openDoc 出站');
});

// 展开态内滚帽（index.html 内联 <style>）：百行 diff 展开不帽就一张卡
// 撑爆整条流——展开后正文自己滚（与气泡全文卡同一读法），流的滚动不被劫走。
test('样式：展开态折叠区有内滚帽（max-height＋overflow-y）', () => {
  const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
  const rule = css.match(/\.mc-foldzone\.mc-foldable:not\(\.folded\) \.mc-body \{[^}]*\}/);
  assert.ok(rule, '展开态 .mc-body 须有独立规则');
  assert.match(rule[0], /max-height:/, '展开态须限高');
  assert.match(rule[0], /overflow-y: auto/, '展开态须自己滚');
});

// 样式面（index.html 内联 <style>）：红删绿增两色行与省略行缺一不可——
// 模板类名与样式规则是两份手抄，漂移即花卡。
test('样式：index.html 带 .kb-diff 红删绿增行与省略行规则', () => {
  const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
  for (const sel of ['.kb-diff-row.del', '.kb-diff-row.add', '.kb-diff-more', '.kb-diff-doc']) {
    assert.ok(css.includes(sel), `app.css 须有 ${sel} 规则`);
  }
  assert.match(css, /\.kb-diff-row\.del[^}]*var\(--bad\)/s, '删行记号用 --bad 红色');
  assert.match(css, /\.kb-diff-row\.add[^}]*var\(--ok\)/s, '增行记号用 --ok 绿色');
  assert.match(css, /\.nc-actions \.btn\[data-kbfull\]:not\(\.loading\)::before/, '看全文钮带外扩热区（:not(.loading) 给转圈 spinner 让位）');
});
