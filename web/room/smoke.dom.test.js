// smoke.dom.test.js — t_183（r_17）：innerHTML 型渲染面的 DOM 冒烟。
// 五面×三底线（渲染不炸/关键节点在/降级不白屏），基座走 testdom.js。
// 断言纪律（评审采纳）：关键类名/结构子串，不全文比对。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from './testdom.js';

// ── 面 1：编年史面板（chronicle）────────────────────────────────

test('chronicle：ready 态渲染时间线（节点在＋条目在）', async () => {
  dom();
  fetchStub([{ body: '2026-10-02｜交付｜小狐｜冒烟第一条\n2026-10-01｜里程碑｜小鹿｜定稿' }]);
  const { ChroniclePanel } = await import('../kb/chronicle.js');
  const p = new ChroniclePanel();
  const root = new El('div');
  await p.load(root); // 不炸即底线①
  assert.equal(p.state, 'ready');
  assert.ok(root.innerHTML.includes('class="chron-list"'), '时间线骨架在（精确类名）');
  assert.ok(root.innerHTML.includes('chron-tp chron-tp-delivery'), '交付标签在（类名对）');
  assert.ok(root.innerHTML.includes('冒烟第一条'), '条目正文在');
  resetDom();
});

test('chronicle：空数据降级不白屏（提示语在）', async () => {
  dom();
  fetchStub([{ body: '只有体例说明没有条目' }]);
  const { ChroniclePanel } = await import('../kb/chronicle.js');
  const p = new ChroniclePanel();
  const root = new El('div');
  await p.load(root);
  assert.equal(p.state, 'empty', '空态');
  assert.ok(root.innerHTML.includes('编年史还没有条目——'), '空态提示在（完整句锚）');
  resetDom();
});

test('chronicle：fetch 失败降级不白屏（错误态＋重试钮在）', async () => {
  dom();
  fetchStub([{ ok: false, status: 500, json: { error: '服务器打盹' } }]);
  const { ChroniclePanel } = await import('../kb/chronicle.js');
  const p = new ChroniclePanel();
  const root = new El('div');
  await p.load(root);
  assert.equal(p.state, 'error');
  assert.ok(root.innerHTML.includes('读取失败'), '错误提示在');
  assert.ok(root.innerHTML.includes('chron-retry'), '重试按钮在');
  resetDom();
});

// ── 面 2：markdown 消息渲染（stream 的底座）────────────────────

test('markdown：正文/粗体/@提及 渲染结构（消息面底座不炸）', async () => {
  const { markdown } = await import('../chat/markdown.js');
  const html = markdown('@小狐 收到 **重点** `code`\n第二行');
  assert.ok(html.includes('<p'), '段落结构在');
  assert.ok(html.includes('<strong>'), '粗体节点在');
  assert.ok(html.includes('@小狐'), '提及正文在');
  const empty = markdown('');
  assert.equal(typeof empty, 'string'); // 空串不抛
});

// ── 面 3：办公室断线气泡 makeBubble（canvas stub）──────────────

test('makeBubble：canvas stub 下泡结构不炸（多行折行）', async () => {
  dom();
  // canvas 2D ctx stub：measureText 返回定宽（折行路径走真逻辑）
  const ctx = {
    font: '',
    measureText: (t) => ({ width: String(t).length * 7 }),
    fillText() {}, strokeText() {},
    fillRect() {}, strokeRect() {},
    beginPath() {}, closePath() {}, moveTo() {}, lineTo() {}, arc() {},
    quadraticCurveTo() {}, rect() {}, roundRect() {},
    fill() {}, stroke() {},
  };
  const { makeBubble } = await import('./bubbles.js');
  const b = makeBubble(ctx, { text: '一行消息\n第二行消息', from: '小狐', ts: 1 }, 800);
  assert.ok(b, '泡对象返回');
  assert.equal(b.lines.length, 2, '两行正文在');
  assert.ok(b.w > 0 && b.h > 0, '泡尺寸在');
  // 超长折行（wrapW 由 viewW 派生）：4 行帽内不炸
  const long = makeBubble(ctx, { text: '很长的消息'.repeat(20), from: '小狐', ts: 1 }, 800);
  assert.ok(long, '长文不抛');
  resetDom();
});

// ── 面 4：名片卡 DetailView（侧栏面板）─────────────────────────

test('DetailView：empty 态提示渲染（不炸不白屏）', async () => {
  dom();
  const { DetailView } = await import('../people/detail.js');
  const side = new El('aside');
  const boardStub = { onPerson: null, toast() {}, ev: {} };
  const v = new DetailView(side, boardStub);
  v.state = 'empty';
  v.render();
  assert.ok(side.innerHTML.includes('side-empty'), '空态提示节点在');
  assert.ok(side.innerHTML.includes('选择一位成员'), '引导文案在');
  resetDom();
});

test('DetailView：error 态降级（服务端坏响应不白屏）', async () => {
  dom();
  const { DetailView } = await import('../people/detail.js');
  const side = new El('aside');
  const v = new DetailView(side, { onPerson: null, toast() {}, ev: {} });
  v.state = 'error';
  v.error = new Error('mock 500');
  v.name = '小狐';
  v.render();
  assert.ok(side.innerHTML.length > 0, '错误态有内容（非白屏）');
  assert.ok(!side.innerHTML.includes('undefined</'), '无 undefined 泄进节点');
  resetDom();
});

// ── 面 5：表情关键词→泡 DOM 面（emoteFromText 的渲染侧）────────

test('表情关键词→泡：命中词渲染出对应 EMOJIS 键的泡底结构', async () => {
  dom();
  const { EMOJIS, emoteFromText } = await import('./emote.js');
  // 冒烟：每款 EMOJIS 有 draw（canvas 面 stub 后可调不炸）
  const ctx = {
    fillStyle: '', strokeStyle: '', font: '', textAlign: '', textBaseline: '', lineWidth: 0,
    globalAlpha: 1,
    measureText: (t) => ({ width: 10 }),
    fillText() {}, strokeText() {}, fillRect() {}, strokeRect() {},
    beginPath() {}, closePath() {}, moveTo() {}, lineTo() {}, arc() {},
    quadraticCurveTo() {}, rect() {}, roundRect() {},
    fill() {}, stroke() {}, save() {}, restore() {},
  };
  for (const [key, v] of Object.entries(EMOJIS)) {
    assert.equal(typeof v.draw, 'function', `${key}.draw 在`);
    v.draw(ctx, 0, 0, 0); // 画不炸＝底线①
  }
  const hit = emoteFromText('谢谢房主');
  assert.ok(EMOJIS[hit], `命中词映射到真实泡键（${hit}）`);
  resetDom();
});
