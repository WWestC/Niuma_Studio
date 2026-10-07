// termboard.dom.test.js — 终端总览的增量渲染契约（第二层 DOM 冒烟）。
// 仓库零依赖纪律不破：不引 happy-dom，手写一棵刚够用的树桩——条目的
// HTML 串不解析（一个条目＝一个持串伪节点），断言的是「哪些节点被换/
// 留/删」的账目，不是布局：
//   - 常态直播帧＝尾部追加：旧节点对象原地不动（marker 钉子）——
//     全量 innerHTML 重建（直播帧每秒数十发 × 上千条）正是本次修的卡顿；
//   - 同 seq 回填＝单卡置换：换的只是那张卡，其余 marker 原样；
//   - 头部插史（水合）落在窗内＝退全量：节点整体换新；
//   - 超帽滑窗＝头部修剪差数＋尾部追加：节点数钉在帽上，窗口在动、
//     账没重建。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom } from '../room/testdom.js';

// tnode：树桩节点。before/remove/appendChild/children 支撑增量账的四步
// （对齐/回填置换/尾部追加/头部修剪）；innerHTML 是字符串槽，template
// 的 content 折叠成单子节点（冒烟面不解析 HTML）。
function tnode(tag = 'div') {
  const n = {
    tag, children: [], parent: null, _html: '', className: '', hidden: false,
    dataset: {}, mark: undefined,
    scrollTop: 50, scrollHeight: 100, clientHeight: 50, offsetHeight: 10,
    set innerHTML(v) { n._html = String(v); n.children = []; n._content = null; },
    get innerHTML() { return n._html; },
    get textContent() { return n._html; },
    set textContent(v) { n._html = String(v); },
    appendChild(c) {
      // 真 DOM 语义：appendChild(fragment) 是把它的孩子摊平接入，不是
      // 挂一个 fragment 节点——增量账的批量追加走的就是这条道。
      if (c.tag === '#fragment') {
        for (const k of c.children.slice()) { k.parent = n; n.children.push(k); }
        c.children = [];
        return c;
      }
      c.parent = n;
      n.children.push(c);
      return c;
    },
    before(...ns) {
      const i = n.parent.children.indexOf(n);
      n.parent.children.splice(i, 0, ...ns);
      ns.forEach((c) => { c.parent = n.parent; });
    },
    remove() {
      if (!n.parent) return;
      const i = n.parent.children.indexOf(n);
      if (i >= 0) n.parent.children.splice(i, 1);
      n.parent = null;
    },
    insertAdjacentHTML(_pos, html) {
      const c = tnode('div');
      c._html = String(html);
      n.appendChild(c);
    },
    querySelector(sel) {
      return ({ '.tl-rail': n._rail, '.tl-head': n._head, '.tl-stream': n._stream,
        '.tl-follow button': n._followBtn })[sel] || null;
    },
    querySelectorAll() { return []; },
    addEventListener() {},
    scrollTo() {},
    get content() {
      if (!n._content) {
        const frag = tnode('#content');
        const child = tnode('div');
        child._html = n._html;
        frag.appendChild(child);
        n._content = frag;
      }
      return n._content;
    },
  };
  return n;
}

// setupBoard：树桩世界＋真 RoomBook（foldTerm 语义不另造）。返回
// { board, stream, entriesOf }——entriesOf 读 stream 里的条目节点
//（跳过 0 号的渲染帽提示条）。
async function setupBoard() {
  dom();
  const doc = globalThis.document;
  doc.createElement = (t) => tnode(t);
  doc.createDocumentFragment = () => tnode('#fragment');
  globalThis.requestAnimationFrame = (fn) => { queueMicrotask(fn); return 0; };
  fetchStub([{ json: { members: [] } }]);
  const { RoomBook } = await import('../chat/rooms.js');
  const { TermBoard } = await import('./termboard.js');
  const book = new RoomBook({ onChange() {}, onNotice() {} });
  book.current = 'default';
  book.rooms.set('default', { key: 'default', name: 'Niuma_Studio', term: new Map(), members: new Map() });
  const el = tnode('div');
  el._rail = tnode('div');
  el._head = tnode('div');
  el._stream = tnode('div');
  el._followBtn = tnode('button');
  el._followBtn.parent = tnode('div');
  const board = new TermBoard(el, book);
  const stream = el._stream;
  const entriesOf = () => stream.children.slice(1);
  return { board, book, stream, entriesOf };
}

const e = (seq, text) => ({ from: '小猿', kind: 'sys', text: text || `s${seq}`, seq, ts: 1790000000 + seq });
const tick = () => new Promise((r) => setTimeout(r, 5));

test('终端板：首画＋尾部追加——旧节点原地不动，不全量重建', async () => {
  const { board, book, stream, entriesOf } = await setupBoard();
  board.revealed();
  await tick();
  book.foldTerm('default', [1, 2, 3, 4, 5].map((s) => e(s)));
  board.onSync('default', 'term');
  await tick();
  assert.equal(entriesOf().length, 5, '首画 5 条');
  const marks = entriesOf().map((n) => { n.mark = `m${n._html}`; return n; });

  book.foldTerm('default', [e(6), e(7)]);
  board.onSync('default', 'term');
  await tick();
  assert.equal(entriesOf().length, 7, '追加后 7 条');
  const now = entriesOf();
  for (let i = 0; i < 5; i++) {
    assert.ok(now[i] === marks[i], `第 ${i + 1} 条节点应是原对象（增量追加，非重建）`);
  }
  assert.ok(now[6]._html.includes('s7'), '新尾在');
  resetDom();
});

test('终端板：同 seq 回填＝单卡置换，其余节点原样', async () => {
  const { board, book, entriesOf } = await setupBoard();
  board.revealed();
  await tick();
  book.foldTerm('default', [1, 2, 3, 4].map((s) => e(s)));
  board.onSync('default', 'term');
  await tick();
  const marks = entriesOf().slice();

  const updated = { ...e(3), text: 's3*（回填终态）' };
  book.foldTerm('default', [updated]);
  board.onSync('default', 'term');
  await tick();
  const now = entriesOf();
  assert.equal(now.length, 4, '条数不变');
  assert.ok(now[0] === marks[0] && now[1] === marks[1], '前两张卡原样');
  assert.ok(now[2] !== marks[2], '回填卡换了新节点');
  assert.ok(now[2]._html.includes('回填终态'), '新内容在');
  assert.ok(now[3] === marks[3], '后一张卡原样');
  resetDom();
});

test('终端板：头部插史落在窗内＝退全量（不漏画历史段）', async () => {
  const { board, book, entriesOf } = await setupBoard();
  board.revealed();
  await tick();
  book.foldTerm('default', [4, 5, 6].map((s) => e(s)));
  board.onSync('default', 'term');
  await tick();
  const marks = entriesOf().slice();

  book.foldTerm('default', [e(1), e(2), e(3)]); // 水合插进头部
  board.onSync('default', 'term');
  await tick();
  const now = entriesOf();
  assert.equal(now.length, 6, '全量后 6 条');
  assert.ok(now.every((n) => !marks.includes(n)), '节点整体换新（全量重建）');
  assert.ok(now[0]._html.includes('s1'), '插进来的历史在头上');
  resetDom();
});

test('终端板：超帽滑窗＝头部修剪＋尾部追加，窗口在动、账不重建', async () => {
  const CAP = 1500;
  const { board, book, stream, entriesOf } = await setupBoard();
  board.revealed();
  await tick();
  // 一次灌 1608 条：增量账从空窗直接长到帽
  const all = [];
  for (let s = 1; s <= 1608; s++) all.push(e(s));
  book.foldTerm('default', all);
  board.onSync('default', 'term');
  await tick();
  assert.equal(entriesOf().length, CAP, 'DOM 钉在渲染帽上');
  assert.ok(stream.children[0]._html.includes('共 1608'), '提示条报真数');
  const probe = entriesOf();
  probe[400].mark = 'pin';

  // 再来 3 条：滑窗——头 3 条被修剪，其余节点（含 pin）原地挪位，不重建
  book.foldTerm('default', [e(1609), e(1610), e(1611)]);
  board.onSync('default', 'term');
  await tick();
  assert.equal(entriesOf().length, CAP, '修剪后仍钉在帽上');
  assert.ok(entriesOf()[397] === probe[400], 'pin 节点原地（头挪 3 位，非重建）');
  assert.ok(entriesOf()[CAP - 1]._html.includes('s1611'), '新尾在');
  assert.ok(stream.children[0]._html.includes('共 1611'), '提示条跟数');
  resetDom();
});
