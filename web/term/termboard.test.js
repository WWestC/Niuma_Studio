// termboard.test.js — 终端总览渲染契约（node --test，零 DOM）：entryHTML
// 的纯函数面——四枚转录专属 kind（input/ask/answer/sys）与承自工作过程
// 的 kind（think/tool/turn）各画各形；总览态带成员名签、聚焦态不带；
// 长文折叠摘要、工具摘要字段优先级。
//
//   node --test web/term/termboard.test.js

import test from 'node:test';
import assert from 'node:assert/strict';
import { entryHTML } from './termboard.js';

// 中文契约面：entryHTML 已是语言敏感输出（ui/i18n.js），裸 Node 的
// navigator.language 是 en-US——先钉 zh 偏好再断言中文形（en 面在
// ui/i18n.test.js 另钉）。t() 在调用时才读偏好，import 后立桩即可。
try {
  globalThis.localStorage = {
    _m: new Map([['dh.ui.prefs', JSON.stringify({ lang: 'zh' })]]),
    getItem(k) { return this._m.has(k) ? this._m.get(k) : null; },
    setItem(k, v) { this._m.set(k, String(v)); },
  };
} catch { /* 已有可写 localStorage 的环境照用 */ }

const e = (o) => ({ from: '小猿', ts: 1790000000, seq: 1, ...o });

test('input 行：短文直出、带来源签；长文折成 details 摘要', () => {
  const html = entryHTML(e({ kind: 'input', src: '房主', text: '跑一下测试' }));
  assert.ok(html.includes('tl-in'));
  assert.ok(html.includes('来自 房主'));
  assert.ok(html.includes('跑一下测试'));
  assert.ok(!html.includes('<details>'));

  const long = entryHTML(e({ kind: 'input', text: 'x'.repeat(900) }));
  assert.ok(long.includes('<details>'));
  assert.ok(/900 字/.test(long));
});

test('ask/answer/sys：问带问文、答带正误色、sys 单行', () => {
  const ask = entryHTML(e({ kind: 'ask', text: '用什么风格？' }));
  assert.ok(ask.includes('tl-ask') && ask.includes('用什么风格？'));
  const bad = entryHTML(e({ kind: 'answer', text: '窗口超时', ok: false }));
  assert.ok(bad.includes('tl-anw bad') || bad.includes('tl-anwbad') || (bad.includes('tl-anw') && bad.includes('bad')));
  const good = entryHTML(e({ kind: 'answer', text: '房主应答：A → 甲', ok: true }));
  assert.ok(good.includes('房主应答'));
  assert.ok(!good.includes('bad'));
  const sys = entryHTML(e({ kind: 'sys', text: '注入失败已排队' }));
  assert.ok(sys.includes('tl-sys') && sys.includes('注入失败已排队'));
});

test('tool：摘要字段优先级（command 先于 path）＋产物折叠＋终态标', () => {
  const html = entryHTML(e({
    kind: 'tool', call: 'c1', tool: 'Bash', state: 'done', ok: true, ms: 1500,
    input: { command: 'go test ./...', file_path: '/tmp/x' },
    output: 'ok pkg 0.1s',
  }));
  assert.ok(html.includes('Bash go test ./...'));
  assert.ok(html.includes('tl-st ok'));
  assert.ok(html.includes('入参'));
  assert.ok(html.includes('产物'));
  assert.ok(html.includes('1.5s'));
});

test('turn：start 分隔线、done 带 stop 与全文回复', () => {
  const start = entryHTML(e({ kind: 'turn', state: 'start' }));
  assert.ok(start.includes('回合开始'));
  const done = entryHTML(e({ kind: 'turn', state: 'done', stop: 'stop', text: '干完了。', ms: 8000 }));
  assert.ok(done.includes('回合结束'));
  assert.ok(done.includes('tl-reply'));
  assert.ok(done.includes('干完了。'));
  assert.ok(done.includes('8.0s'));
});

test('总览态带成员名签，聚焦态不带；think 折叠摘要', () => {
  const who = entryHTML(e({ kind: 'sys', text: 'x' }), { who: true });
  assert.ok(who.includes('tl-who') && who.includes('小猿'));
  const solo = entryHTML(e({ kind: 'sys', text: 'x' }));
  assert.ok(!solo.includes('tl-who'));
  const think = entryHTML(e({ kind: 'think', text: '先想想要不要动索引……'.repeat(30) }));
  assert.ok(think.includes('<details>'));
  assert.ok(/共 \d+ 字/.test(think));
});

test('未知 kind 与空条目安全让位', () => {
  assert.equal(entryHTML(e({ kind: 'mystery' })), '');
  assert.equal(entryHTML(null), '');
});

test('回合回复走 markdown 渲染（加粗/代码）', () => {
  const html = entryHTML(e({ kind: 'turn', state: 'done', stop: 'stop', text: '**结论**：走 `go test`' }));
  assert.ok(html.includes('tl-md'));
  assert.ok(html.includes('<strong>结论</strong>'));
  assert.ok(html.includes('<code'));
});

test('工具产物走 markdown；入参图片经窄端点直出', () => {
  const html = entryHTML(e({
    kind: 'tool', call: 'c1', tool: 'Read', state: 'done', ok: true,
    input: { file_path: '/tmp/shot.png' },
    output: '# 标题\n\n正文 **加粗**',
  }), { key: 'proj-x' });
  assert.ok(html.includes('tl-md'));
  assert.ok(html.includes('<h1>标题</h1>') || html.includes('标题'));
  assert.ok(html.includes('<strong>加粗</strong>'));
  assert.ok(html.includes('tl-img'));
  assert.ok(html.includes('/p/proj-x/trace/file?path='));

  // 无 key（纯函数面）不出图也不炸。
  const bare = entryHTML(e({
    kind: 'tool', call: 'c1', tool: 'Read', state: 'done',
    input: { file_path: '/tmp/shot.png' }, output: 'x',
  }));
  assert.ok(!bare.includes('tl-img'));
});

test('注入行按终端原样直出（不渲染 markdown）', () => {
  const html = entryHTML(e({ kind: 'input', src: '房主', text: '**不是渲染** 是原文' }));
  assert.ok(html.includes('<pre>'));
  assert.ok(!html.includes('<strong>'));
});
