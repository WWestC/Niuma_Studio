// zladder.dom.test.js — 全局 z-index 梯子的契约（siderail.dom.test.js
// 读 app.css 断言的同款写法）。起因：项目空间面板（#space-gate z 96，
// 不透明全屏）里发起的删除确认对话框（.fb-dlg-wrap）曾落在 z 90——弹
// 窗开了但被面板整块盖住，「按了没反应」；成功/失败 toast（#toasts
// z 95）同样被盖。梯子由此立契：
//
//   内容浮层（≤90：启动门/设置窗/图片查看…）
//     < 项目空间面板 96
//     < 对话框/抽屉 100（模态要压过一切面板）
//     < 全局 toast 105（写操作回执面板开着也要看得见）
//     < select/时钟弹层 110（对话框表单里的控件弹层要盖住宿主）
//     < 引导层 120（新手引导压一切）
//
// 谁想挪任何一格，先过这里——层级是隐形行为，DOM 测试测不到，只能
// 用数字钉死。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');

const escRe = (s) => s.replaceAll('.', '\\.').replaceAll('[', '\\[').replaceAll(']', '\\]')
  .replaceAll('(', '\\(').replaceAll(')', '\\)').replaceAll('#', '\\#');

// 取某选择器规则块里的 z-index 数字（找不到即断言失败——选择器改名
// 也该顺带改这里的契约）。
function zOf(sel) {
  const m = css.match(new RegExp(`${escRe(sel)}\\s*\\{[^}]*z-index:\\s*(\\d+)`));
  assert.ok(m, `${sel} 应有一条带 z-index 的规则（选择器改名了？契约要跟着改）`);
  return Number(m[1]);
}

test('模态族（对话框/抽屉）压过项目空间面板——面板里发起的确认弹窗要看得见', () => {
  const gate = zOf('#space-gate');
  assert.ok(zOf('.fb-dlg-wrap') > gate, '.fb-dlg-wrap 必须高于 #space-gate（曾因 90<96 被不透明面板盖住）');
  assert.ok(zOf('.fb-drawer-wrap') > gate, '.fb-drawer-wrap 同模态族，同层纪律');
});

test('全局 toast 压过对话框与项目空间面板——写操作回执不进盲区', () => {
  const gate = zOf('#space-gate');
  const dlg = zOf('.fb-dlg-wrap');
  const toasts = zOf('#toasts');
  assert.ok(toasts > gate, '#toasts 必须高于 #space-gate（面板里删除的成/败回执曾被盖住）');
  assert.ok(toasts > dlg, '#toasts 高于对话框（toast 盖模态的既有关系，保持）');

});

test('select/时钟弹层压过对话框——对话框表单里的控件弹层要盖住宿主', () => {
  const dlg = zOf('.fb-dlg-wrap');
  assert.ok(zOf('.sel-pop') > dlg, '.sel-pop 必须高于对话框（select 全局接管，对话框里也会长 select）');
  assert.ok(zOf('.clock-pop') > dlg, '.clock-pop 与 sel-pop 同档（时间输入弹层）');
});

test('引导层压过 toast——新手引导仍是最高常设层', () => {
  assert.ok(zOf('.guide-layer') > zOf('#toasts'), '.guide-layer（120 档）压过 toast');
});
