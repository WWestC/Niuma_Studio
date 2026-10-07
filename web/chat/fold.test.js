// fold.test.js — 长内容自动折叠（飞书式，fold.js）的契约钉：折叠区
// DOM 形状（.mc-foldzone > .mc-body + [data-mcfold]）、量高定折（超线
// 才挂折叠态，短文零铬面）、幂等（已定折的不改判——用户手动展开过的
// 不被重跑翻回去）、触发钮翻转（展开 ↔ 收起换文案）。量高用桩元素
// （scrollHeight 一读、classList 一写），DOM 树不进测试——fold.js 本就
// 只碰这三个面。另钉三处接线的源形状（stream.js / taskcard.js /
// index.html）：模板类名、CSS 选择器、包装点是两份手抄，漂移即丢折叠。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { foldZoneHTML, wireFoldZone, wireFoldZones, toggleFoldBtn, FOLD_LIMIT } from './fold.js';
import { taskCardHTML } from './taskcard.js';
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


// —— 桩元素：class 集 + 量高一读 + querySelector 认两个已知槽 ——
const stubZone = (scrollH, state = []) => {
  const btn = { innerHTML: '' };
  const body = { scrollHeight: scrollH };
  const zone = {
    state: new Set(state),
    classList: {
      add: (...cs) => cs.forEach((c) => zone.state.add(c)),
      remove: (...cs) => cs.forEach((c) => zone.state.delete(c)),
      contains: (c) => zone.state.has(c),
      toggle: (c) => (zone.state.has(c) ? (zone.state.delete(c), false) : (zone.state.add(c), true)),
    },
    querySelector: (sel) => (sel === '.mc-body' ? body : sel === '.mc-fold' ? btn : null),
    querySelectorAll: () => [],
  };
  btn.closest = (sel) => (sel === '.mc-foldzone' ? zone : null);
  return { zone, body, btn };
};

test('折叠区形状：.mc-foldzone > .mc-body＋[data-mcfold] 触发钮（初值「展开」）', () => {
  const html = foldZoneHTML('<p>正文</p>');
  assert.match(html, /^<div class="mc-foldzone"><div class="mc-body"><p>正文<\/p><\/div>/);
  assert.match(html, /<button type="button" class="mc-fold" data-mcfold>展开 /);
  assert.ok(html.includes('<svg'), '钮带下箭头图标（icon 词汇）');
  assert.ok(!html.includes('mc-foot'), '无尾件不生区脚行（裸钮旧形）');
});

// 区脚同行（tail）：黑板卡的「看全文」要与「展开」同排——尾件与触发钮
// 同住 .mc-foot 一行（钮居左、尾件居右，CSS 管）；无尾件的宿主零感知。
test('区脚同行：带尾件时触发钮与尾件同住 .mc-foot，尾件在钮后', () => {
  const html = foldZoneHTML('<p>正文</p>', '<div class="nc-actions">尾件</div>');
  assert.match(html, /<div class="mc-foot"><button type="button" class="mc-fold" data-mcfold>/, '区脚行以触发钮开头（居左）');
  assert.ok(html.indexOf('data-mcfold') < html.indexOf('nc-actions'), '尾件在钮后（居右）');
  assert.match(html, /nc-actions[\s\S]*<\/div><\/div>$/, '尾件收在区脚行内、随折叠区闭合');
});

test('量高定折：正文高超线才收起（挂 folded＋钮翻「展开」），短文零铬面', () => {
  const tall = stubZone(FOLD_LIMIT + 1);
  assert.equal(wireFoldZone(tall.zone), true, '超线须新挂折叠');
  assert.ok(tall.zone.state.has('mc-foldable') && tall.zone.state.has('folded'));
  assert.match(tall.btn.innerHTML, /^展开 /);

  const short = stubZone(FOLD_LIMIT);
  assert.equal(wireFoldZone(short.zone), false, '恰好在线上不折（预览高留有余量）');
  assert.ok(!short.zone.state.has('mc-foldable'), '短文不挂类——钮不显形');

  const broken = stubZone(999);
  broken.zone.querySelector = () => null; // 缺 .mc-body/.mc-fold 的残区
  assert.equal(wireFoldZone(broken.zone), false, '残区安全跳过不炸');
  assert.equal(wireFoldZone(null), false, 'null 安全跳过');
});

test('幂等：已定折的区重跑不改判——手动展开过的（foldable 在场）不被翻回去', () => {
  const z = stubZone(FOLD_LIMIT * 3, ['mc-foldable']); // 用户已展开（folded 已摘）
  assert.equal(wireFoldZone(z.zone), false, 'foldable 在场即跳过');
  assert.ok(!z.zone.state.has('folded'), '不许把展开态重新收起');

  const fresh = stubZone(FOLD_LIMIT + 5);
  wireFoldZone(fresh.zone);
  fresh.zone.state.delete('folded'); // 手动展开
  wireFoldZone(fresh.zone); // 图片后到重量高等场景的重跑
  assert.ok(!fresh.zone.state.has('folded'), '重跑只补新判不翻旧账');
});

test('翻转钮：整区翻折叠态并换文案（展开 ↔ 收起）', () => {
  const { zone, btn } = stubZone(0, ['mc-foldable']);
  assert.equal(toggleFoldBtn(btn), true, '第一次点＝收起');
  assert.ok(zone.state.has('folded'));
  assert.match(btn.innerHTML, /^展开 /);
  assert.equal(toggleFoldBtn(btn), false, '第二次点＝展开');
  assert.ok(!zone.state.has('folded'));
  assert.match(btn.innerHTML, /^收起 /);

  const stray = { innerHTML: '', closest: () => null }; // 钮不在任何区里
  assert.equal(toggleFoldBtn(stray), false, '无区可翻安全返回');
});

test('wireFoldZones：一片范围内的区都量一遍（新判补上，已判不动）', () => {
  const tall = stubZone(FOLD_LIMIT + 1);
  const short = stubZone(10);
  const decided = stubZone(FOLD_LIMIT * 2, ['mc-foldable']);
  const before = [...decided.zone.state];
  const root = { querySelectorAll: () => [tall.zone, short.zone, decided.zone] };
  wireFoldZones(root);
  assert.ok(tall.zone.state.has('folded'), '超线的补挂折叠');
  assert.ok(!short.zone.state.has('mc-foldable'), '短的不动');
  assert.deepEqual([...decided.zone.state], before, '已定折的原样');
  wireFoldZones(null); // null 根安全
});

// —— 任务卡接入：描述是卡里唯一会长的部位，折叠区接管旧的
// line-clamp:2 硬裁（裁掉的字再也没处看）；操作钮在折叠区外照常可点。
test('任务卡：长描述包折叠区，操作钮（data-op）留在区外', () => {
  const html = taskCardHTML({
    id: 't_1', title: '任务', status: 'todo',
    desc: '很长'.repeat(60),
    assignee: '小马',
  }, { viewer: '房主' });
  assert.match(html, /<div class="tk-desc"><div class="mc-foldzone">/);
  assert.match(html, /class="mc-fold" data-mcfold>展开 /);
  assert.ok(html.indexOf('</div></div>') < html.indexOf('tk-ops'), '折叠区先闭合，操作行在其外');
  assert.match(html, /data-op="start"/, '操作钮不受折叠影响');

  const noDesc = taskCardHTML({ id: 't_2', title: '短任务', status: 'todo' });
  assert.ok(!noDesc.includes('mc-foldzone'), '无描述不长折叠区');
});

// —— 接线源扫描（stream.js 无 DOM 测试基建，interrupted.test.js 先例）——
// 三类宿主必须真的把正文包进 foldZoneHTML：对话/汇报气泡（#talkRow）、
// 通知卡（#noticeRow 的黑板 diff 与 nc-body 播报）；点击面 [data-mcfold]
// 分支须先于 .tk-card（任务描述的钮在卡内，晚了会被「点卡开弹层」截走）。
test('接线：stream.js 三宿主包 foldZoneHTML，折叠分支先于 tk-card 弹层', () => {
  const src = readFileSync(new URL('./stream.js', import.meta.url), 'utf8');
  assert.match(src, /import \{ foldZoneHTML, wireFoldZones, toggleFoldBtn \} from '\.\/fold\.js';/);
  // 对话/汇报：引用块＋正文＋图片整体住折叠区，操作条在外
  assert.match(src, /foldZoneHTML\(quoteEl \+ `<div class="msg-text">/, 'say 正文进折叠区');
  assert.match(src, /foldZoneHTML\(atTag \+ richHTML\(text\)\)/, '汇报正文进折叠区');
  // 通知卡：黑板 diff 与纯文本播报都进折叠区
  assert.match(src, /body = foldZoneHTML\(kbDiffHTML\(frame\),/, '黑板 diff 进折叠区（看全文钮作区脚尾件同行）');
  assert.match(src, /body = foldZoneHTML\(`<div class="nc-body">/, '纯文本播报进折叠区');
  // 点击序：[data-mcfold] 先于 .tk-card（否则任务描述的展开钮开弹层）
  assert.ok(src.indexOf("t.closest('[data-mcfold]')") < src.indexOf("t.closest('.tk-card')"),
    '折叠钮分支须在 tk-card 分支之前');
  // 量高口：#wireFold 走共享 wireFoldZones（行插入/图片加载/任务重绘都经它）
  assert.match(src, /#wireFold\(row\) \{\s*\n\s*wireFoldZones\(row\);/);
  assert.match(src, /taskOutcome[\s\S]*?wireFoldZones\(fresh\);/, '任务重绘后的新卡重量高');
});

// —— 样式面（index.html 内联 <style>）：选择器已脱离 .bubble 作用域
// （泡/通知卡/任务描述裸用）；旧的任务描述硬裁（line-clamp:2）须已退场。
test('样式：折叠选择器按 .mc-foldzone 泛化，任务描述硬裁已撤', () => {
  const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
  for (const sel of [
    '.mc-foldzone.mc-foldable.folded .mc-body',
    '.mc-foldzone.mc-foldable .mc-fold',
    '.mc-foldzone.mc-foldable:not(.folded) .mc-fold',
  ]) {
    assert.ok(css.includes(sel), `app.css 须有 ${sel} 规则`);
  }
  assert.ok(!css.includes('.bubble.mc-foldable'), '旧 .bubble 作用域选择器须已泛化');
  assert.ok(css.includes('.mc-fold::before'), '展开/收起钮带外扩热区（伪元素，钮太瘦难点中）');
  const tkDesc = css.match(/\.tk-desc \{[^}]*\}/s); // 别处的 line-clamp（toast/
  assert.ok(tkDesc, '.tk-desc 规则须在'); // 横幅/需求卡）不归折叠管，只钉这一条
  assert.ok(!tkDesc[0].includes('-webkit-line-clamp'), '任务描述的硬裁须已撤（折叠区接管）');
  assert.match(css, /\.mc-foldzone\.mc-foldable\.folded \.mc-body[^}]*max-height: 132px/s, '收起预览 132px＋渐隐幕');
});
