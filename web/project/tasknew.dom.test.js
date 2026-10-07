// tasknew.dom.test.js — 新建任务浮层 v3（飞书建卡式）的结构冒烟：
// 无头卡（✕ 独占右上，旧版标题条退役）、标题井/描述井、图标字段行
// 四件（负责人/时间/优先级/所属只读行）、创建键空标题禁用（渐进点亮）、
// roster 选项带离职注记、注入面 esc。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom } from '../room/testdom.js';

test('tasknew：飞书建卡式结构——标题井＋字段行四件＋创建键禁用', async () => {
  dom();
  const { TaskNewForm } = await import('./tasknew.js');
  new TaskNewForm({
    projectName: 'Niuma_Studio',
    roster: ['小马', '老王'],
    tagOf: (n) => (n === '老王' ? '已离职' : ''),
    onSubmit: () => {},
  });
  const overlay = globalThis.document.body.children.at(-1);
  assert.ok(overlay && overlay.className === 'overlay', '浮层挂到 body');
  const html = overlay._html;
  // 无头卡：✕ 是头部唯一件（旧版 <strong> 标题条退役——标题井即主角）
  assert.ok(html.includes('data-close'), '右上关闭在');
  assert.ok(!html.includes('<strong>'), '无头卡——旧版标题条退役');
  assert.ok(html.includes('tn-card'), '样式作用域类在');
  // 标题井/描述井（无边框大字输入）
  assert.ok(html.includes('tn-title') && html.includes('data-title'), '标题井在');
  assert.ok(html.includes('tn-desc') && html.includes('data-desc'), '描述井在');
  // 字段行四件：负责人（roster＋离职注记）/时间成对/优先级/所属只读
  assert.ok(html.includes('负责人') && html.includes('任务池（待认领）'), '负责人行含任务池可空位');
  assert.ok(html.includes('小马'), 'roster 注入');
  assert.ok(html.includes('老王（已离职）'), '离职注记跟随选项');
  assert.ok(html.includes('data-start') && html.includes('data-end'), '时间行成对');
  assert.ok(html.includes('data-priority') && html.includes('紧急'), '优先级行含档位');
  assert.ok(html.includes('tn-own-name'), '所属只读行在');
  // 创建键：空标题禁用（渐进点亮，飞书同款）
  assert.match(html, /data-ok[^>]*disabled/, '空标题创建键禁用');
  assert.ok(html.includes('data-hint'), '页脚快捷提示槽在');
  resetDom();
});

test('tasknew：注入面 esc＋close 收尾不炸', async () => {
  dom();
  const { TaskNewForm } = await import('./tasknew.js');
  const form = new TaskNewForm({ projectName: '<b>x</b>', onSubmit: () => {} });
  const overlay = globalThis.document.body.children.at(-1);
  assert.ok(overlay._html.includes('&lt;b&gt;'), '项目名已 esc');
  assert.ok(!overlay._html.includes('<b>x</b>'), '裸 HTML 不穿透');
  form.close();
  assert.equal(form.el, null, 'close 摘句柄');
  resetDom();
});
