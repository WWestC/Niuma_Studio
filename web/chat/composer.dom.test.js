// composer.dom.test.js — 输入井量高的藏/露契约（一条缝之夜的钉）：
// 侧栏办公室列表在任何板块都能切房，藏着切过来的那一刻对话板块还是
// display:none，#grow 量 scrollHeight 得 0——把 0 写成高度，输入井就
// 被压成一条缝（重启后不进大厅直接切项目房即中招）。契约两面：藏着
// 量不出时不写 0 高度（保持 'auto' 自然行高兜底）；露面（revealed，
// app.js 的 route() 喂）补量，把真正的内容高度写回去。
// 斜杠命令菜单（/ 技能附着与管理动词）同在此驱动：onInput 是 input
// 事件的公共缝，菜单渲染与词元补全（insertSlash）都从这里过。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, El, fetchStub } from '../room/testdom.js';

// Composer 构造要的全套节点（testdom El：style 是普通对象，scrollHeight
// 由用例手动摆——它就是要测的那个量的真伪；selectionStart/End 与
// setSelectionRange 由用例自设，真浏览器里设 value 归位光标的行为
// stub 不模拟）
function makeEls() {
  const input = new El('textarea');
  return {
    input,
    root: new El('div'), send: new El('button'), pick: new El('div'),
    status: new El('span'), count: new El('span'), quote: new El('div'),
    at: new El('button'), emoji: new El('button'), emojiPick: new El('div'),
    image: new El('button'), file: new El('input'), thumbs: new El('div'),
    outdent: new El('button'), indent: new El('button'),
  };
}

async function composerWith() {
  dom();
  const { Composer } = await import('./composer.js');
  const els = makeEls();
  const book = { current: 'lobby' };
  return { composer: new Composer(els, book, {}), els };
}

test('藏着切房量不出：不写 0 高度，露面补量', async () => {
  const { composer, els } = await composerWith();
  // 藏着（display:none → scrollHeight＝0）切进一间项目房
  els.input.scrollHeight = 0;
  composer.onChange(null, 'switch');
  assert.notEqual(els.input.style.height, '0px', '0 高度＝一条缝');
  assert.equal(els.input.style.height, 'auto', '量不出时回落自然行高');
  // 露面（route() → revealed()）：此刻量得出，写回真高度
  els.input.scrollHeight = 66;
  composer.revealed();
  assert.equal(els.input.style.height, '66px');
});

test('可见时照旧量高并封顶 140', async () => {
  const { composer, els } = await composerWith();
  els.input.scrollHeight = 44;
  composer.onChange(null, 'switch');
  assert.equal(els.input.style.height, '44px');
  els.input.scrollHeight = 999;
  composer.revealed();
  assert.equal(els.input.style.height, '140px', '封顶与 CSS max-height 同源');
});

test('引用条的摘录不搬旧式引用头——结构化引用的 snip 从源头干净', async () => {
  const { composer } = await composerWith();
  // 引用一条存量带引用头的回复：头（连同其中的 @李四）剥掉，只留正文
  composer.quoteFrom({ from: '小苗', seq: 1791011780496,
    text: '「引用 #12 @李四：方案 B 定稿」\n同意，按这个来' });
  assert.equal(composer.quote.from, '小苗');
  assert.equal(composer.quote.seq, 1791011780496);
  assert.equal(composer.quote.text, '同意，按这个来');
  // 新结构化消息正文天生干净：原样入摘要
  composer.quoteFrom({ from: '小鹿', seq: 7, text: '深色模式跟主色一起出。' });
  assert.equal(composer.quote.text, '深色模式跟主色一起出。');
});

test('缩进 ←→（ZCode 式）：无选区垫光标处、盖行整块垫/剥，← 钮跟着亮灭', async () => {
  const { composer, els } = await composerWith();
  composer.setIdentity('房主'); // 写面打开：→ 亮、← 视井内有无前导空白
  assert.equal(els.indent.disabled, false);
  assert.equal(els.outdent.disabled, true, '空井没缩进可剥：← 灰');
  // → 的腿（Tab 同源）：光标处垫两空格，光标落空格后
  els.input.value = '第一行\n第二行';
  els.input.setSelectionRange(0, 0);
  composer.indent(1);
  assert.equal(els.input.value, '  第一行\n第二行');
  assert.equal(els.input.selectionStart, 2);
  assert.equal(els.input.selectionEnd, 2);
  assert.equal(els.outdent.disabled, false, '垫过就有得剥：← 亮');
  // ← 的腿（Shift+Tab 同源）：剥回原样
  composer.indent(-1);
  assert.equal(els.input.value, '第一行\n第二行');
  // 多行选区：盖到的行整块各垫一层
  els.input.setSelectionRange(0, 7); // 盖满两行（'第一行\n第二行' 共 7 字）
  composer.indent(1);
  assert.equal(els.input.value, '  第一行\n  第二行');
  // 剥无可剥（indentLines 返 null）：原样不动
  composer.indent(-1);
  composer.indent(-1);
  assert.equal(els.input.value, '第一行\n第二行');
  assert.equal(els.outdent.disabled, true, '剥平后 ← 灭');
});

test('只读脸：←→ 一起灰，indent() 不动作', async () => {
  const { composer, els } = await composerWith();
  composer.setIdentity(null); // 未识别房主——只读
  assert.equal(els.indent.disabled, true);
  assert.equal(els.outdent.disabled, true);
  els.input.value = '  x';
  els.input.setSelectionRange(4, 4);
  composer.indent(-1);
  composer.indent(1);
  assert.equal(els.input.value, '  x', '只读不缩进');
});

// --- 斜杠命令菜单（/ 技能附着与管理动词）-------------------------------

const slashComposer = async () => {
  dom();
  // 技能库读面：菜单懒拉一次 GET /skills（60s 缓存，本文件所有用例共用）。
  // 载荷形态＝裸对象直当响应体：这里回数组本身。
  fetchStub([[
    { key: 'review', name: '代码评审' },
    { key: 'writer', name: '文案手感' },
  ]]);
  const { Composer } = await import('./composer.js');
  const els = makeEls();
  const book = { current: 'lobby', mentionCandidates: () => [], groupCandidates: () => [] };
  return { composer: new Composer(els, book, {}), els };
};

const settle = () => new Promise((r) => setTimeout(r, 5));

test('打 / 弹菜单：管理动词置顶，技能库随后（懒拉到位后）', async () => {
  const { composer, els } = await slashComposer();
  els.input.value = '/';
  els.input.setSelectionRange(1, 1);
  composer.onInput();
  assert.equal(els.pick.hidden, false, '菜单应弹出');
  let html = els.pick.innerHTML;
  assert.match(html, /data-slash="skill new"/, '管理动词应在榜');
  // 技能列表是懒拉的：等 fetch 落地后再敲一键，技能进榜
  await settle();
  els.input.value = '/re';
  els.input.setSelectionRange(3, 3);
  composer.onInput();
  html = els.pick.innerHTML;
  assert.match(html, /data-slash="review"/, '前缀命中的技能应进榜');
  assert.doesNotMatch(html, /data-slash="writer"/, '未命中前缀的技能不进榜');
  assert.doesNotMatch(html, /data-slash="skill new"/, '动词不命中 /re 前缀');
});

test('insertSlash：词元片段替换为 /token＋尾空格，光标落词尾后', async () => {
  const { composer, els } = await slashComposer();
  els.input.value = '帮我 /rev @小马';
  els.input.setSelectionRange(7, 7); // 光标在 "/rev" 后
  composer.slashStart = 3; // #syncSlash 会摆；这里直测插入腿，手动摆位
  composer.insertSlash('review');
  assert.equal(els.input.value, '帮我 /review  @小马');
  assert.equal(els.input.selectionStart, 11, '光标落尾空格后');
  assert.equal(els.pick.hidden, true, '插入后菜单收起');
  // 管理动词同腿：/sk → /skill new （用户接着敲参数）
  els.input.value = '/sk';
  composer.slashStart = 0;
  composer.insertSlash('skill new');
  assert.equal(els.input.value, '/skill new ');
});

test('URL 与句中斜杠不误伤：只有 /＋key 词元的片段才弹菜单', async () => {
  const { composer, els } = await slashComposer();
  await settle(); // 技能缓存就位
  // http://x/a 的斜杠骑在单词文本上——不触发
  els.input.value = '看下 http://127.0.0.1:7777/app';
  els.input.setSelectionRange(29, 29);
  composer.onInput();
  assert.equal(els.pick.hidden, true, 'URL 不弹菜单');
  // 行中的 /（前面是空白）且 stem 无命中——不弹（词表里没有该 key）
  els.input.value = '先 /zzz 再说';
  els.input.setSelectionRange(8, 8);
  composer.onInput();
  assert.equal(els.pick.hidden, true, '无命中应收菜单');
  // @ 名单不受影响：@ 优先于 /
  els.input.value = '@';
  els.input.setSelectionRange(1, 1);
  composer.onInput();
  assert.equal(composer.pickMode, 'at', '@ 优先探测');
});
