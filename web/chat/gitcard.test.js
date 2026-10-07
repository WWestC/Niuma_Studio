// gitcard.test.js — 版本管理事件流两张卡的模板钉（v2.8 gitflow）：
// 提交卡（gitCommitHTML）——sha 短缩＋摘要一行、逐文件红绿行、合计
// 行、引用台账号小签；合并卡（mergeCardHTML）——分支→主线一行、
// 红绿合计、涉及任务号，待审＋房主视角出 验收并入/驳回 按钮。正文
// 一律转义（提交信息/路径是牛马手笔，不能带 HTML 进气泡）；无载荷 →
// 空串（调用方退回旧的事件词渲染）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { gitCommitHTML, mergeCardHTML } from './gitcard.js';
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


test('提交卡：短 sha＋摘要、逐文件红绿行、合计、引用小签', () => {
  const html = gitCommitHTML({ git: {
    sha: 'abc1234def567890abcdef1234567890abcdef12',
    subject: 'feat(core): 关单验提交',
    files: [
      { path: 'server/gitflow.go', added: 120, removed: 3 },
      { path: 'vcs/flow.go', added: 88, removed: 0 },
    ],
    additions: 208, deletions: 3, refs: ['t_190', 'r_18'],
  } });
  assert.match(html, /<code>abc1234<\/code>/, 'sha 只展示前 7 位');
  assert.match(html, /feat\(core\): 关单验提交/);
  assert.match(html, /<span class="gc-add">\+120<\/span><span class="gc-del">−3<\/span>/);
  assert.match(html, /server\/gitflow\.go/);
  assert.match(html, /共 2 文件/);
  assert.match(html, /<span class="gc-ref">t_190<\/span>/);
  assert.match(html, /<span class="gc-ref">r_18<\/span>/);
});

test('提交卡：文件超 6 个折叠加计数；二进制文件不出红绿数', () => {
  const files = Array.from({ length: 8 }, (_, i) => ({ path: `f${i}.go`, added: 1, removed: 0 }));
  files.splice(1, 0, { path: 'logo.png', binary: true }); // 展示窗口内，验证行的渲染
  const html = gitCommitHTML({ git: { sha: '1234567abcd', files } });
  assert.match(html, /…另有 3 个文件/, '9 个文件只列 6 个，其余计数');
  assert.match(html, /<span class="gc-bin">二进制<\/span>/);
  assert.match(html,
    /<div class="gc-file"><span class="gc-stat"><span class="gc-bin">二进制<\/span><\/span><span class="gc-path">logo\.png<\/span><\/div>/,
    'logo.png 整行是「二进制」记号，无 +N/−N');
});

test('提交卡：正文转义——提交信息与路径里的 HTML 永不裸进气泡', () => {
  const html = gitCommitHTML({ git: {
    sha: 'ffff0000ffff', subject: '<script>x</script>「&」',
    files: [{ path: 'a<b>.go', added: 1, removed: 0 }],
  } });
  assert.ok(!html.includes('<script>'), '标签必须被转义');
  assert.match(html, /&lt;script&gt;/);
  assert.match(html, /a&lt;b&gt;\.go/);
});

test('提交卡：无 git 载荷返回空串（denied/旧服务端退回旧渲染）', () => {
  assert.equal(gitCommitHTML({}), '');
  assert.equal(gitCommitHTML({ git: null, text: 'x' }), '');
});

test('合并卡：分支→主线一行＋红绿合计＋涉及任务号', () => {
  const html = mergeCardHTML({ event: 'submitted', merge: {
    id: 'm_01', branch: 'dev-x', into: 'main',
    commits: 3, files: 5, additions: 120, deletions: 30, tasks: ['t_184', 't_185'],
  } });
  assert.match(html, /<span class="gm-branch">dev-x<\/span>/);
  assert.match(html, /<span class="gm-arrow">→<\/span><span class="gm-into">main<\/span>/);
  assert.match(html, /<span class="gm-id">m_01<\/span>/);
  assert.match(html, /3 提交 · 5 文件/);
  assert.match(html, /<span class="gc-ref">t_184<\/span>/);
});

test('合并卡：待审＋房主视角出验收/驳回按钮；其余视角与已审事件不出', () => {
  const base = { event: 'submitted', merge: { id: 'm_01', branch: 'dev', into: 'main' } };
  assert.match(mergeCardHTML(base, { viewer: '房主' }), /data-merge-accept/);
  assert.match(mergeCardHTML(base, { viewer: '房主' }), /data-merge-reject/);
  assert.ok(!mergeCardHTML(base, {}).includes('data-merge-accept'), '无 viewer（成员/旧调用）不出按钮');
  const merged = { ...base, event: 'merged' };
  assert.ok(!mergeCardHTML(merged, { viewer: '房主' }).includes('data-merge-accept'), '已并入的卡是历史回响，不出按钮');
  assert.equal(mergeCardHTML({}), '', '无 merge 载荷空串');
});

// stream.js 无 DOM 测试基建（kbdiff.test.js 先例）：接线只能源扫描——
// Msg.Git/Msg.Merge 必须真的进 #noticeRow（分派＋body 分支＋房主按钮的
// #onClick 处理），wire 常量两端对齐；任一处被删，帧就静默不可见。
test('接线：分派/body/按钮三处都在，wire 常量对齐', () => {
  const src = readFileSync(new URL('./stream.js', import.meta.url), 'utf8');
  assert.match(src, /import \{ gitCommitHTML, mergeCardHTML \} from '\.\/gitcard\.js';/, '须引入 gitcard 模块');
  assert.match(src, /case Msg\.Git:/, '帧分派须含 Msg.Git');
  assert.match(src, /case Msg\.Merge:/, '帧分派须含 Msg.Merge');
  assert.match(src, /body = foldZoneHTML\(gitCommitHTML\(frame\)\);/, '提交卡正文挂进通知体（带折叠）');
  assert.match(src, /body = mergeCardHTML\(frame, \{ viewer: this\.ownerName \|\| '' \}\);/, '合并卡带房主视角');
  assert.match(src, /\[data-merge-accept\],\[data-merge-reject\]/, '#onClick 须处理两枚按钮');
  const wire = readFileSync(new URL('../wire/wire.js', import.meta.url), 'utf8');
  for (const k of ['Git:', 'MergeSubmit:', 'MergeAccept:', 'MergeReject:', 'Merge:']) {
    assert.ok(wire.includes(k), `wire.js 须有 ${k} 常量`);
  }
});

// 样式面（index.html 内联 <style>）：提交/合并卡与台账号小签缺一不可
// ——模板类名与样式规则是两份手抄，漂移即花卡。
test('样式：index.html 带 git-commit/git-merge/gc-ref 规则', () => {
  const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
  for (const sel of ['.git-commit', '.git-merge', '.gc-subject', '.gc-file', '.gc-tally', '.gc-refs', '.gc-ref', '.gm-line', '.gm-arrow']) {
    assert.ok(css.includes(sel), `app.css 须有 ${sel} 规则`);
  }
  assert.match(css, /\.gc-stat \.gc-add, \.gc-tally \.gc-add[^}]*var\(--ok\)/s, '增行数用 --ok 绿色');
  assert.match(css, /\.gc-stat \.gc-del, \.gc-tally \.gc-del[^}]*var\(--bad\)/s, '删行数用 --bad 红色');
});
