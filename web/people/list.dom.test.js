// list.dom.test.js — 花名册项目列＋筛选＋搜索的冒烟（testdom 第一层：
// innerHTML 字符串面断言，不进事件路由——input/change 监听在 El 世界是
// no-op，过滤行为经 render() 直测）。项目真源是 PersonSummary.room
// （成员当前座位：default=大厅、空=未入座）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub } from '../room/testdom.js';

const PROJECTS = [
  { key: 'default', name: 'Niuma_Studio', status: 'active' },
  { key: 'alpha', name: '甲项目', status: 'active' },
  { key: 'beta', name: '乙项目', status: 'archived' },
];

// board 替身：项目列查 board.projects（名字解析），下拉喝 projectChoices()
function board() {
  return {
    projects: PROJECTS,
    projectChoices: () => [{ key: 'default', name: 'Niuma_Studio' }, { key: 'alpha', name: '甲项目' }],
    selected: null,
    openRecruit() {},
  };
}

const PEOPLE = [
  { name: '阿甲', role: '编排者', online: true, room: 'default', rank: 3 },
  { name: '小乙', role: 'Backend Dev', online: true, room: 'alpha', rank: 2 },
  { name: '老丙', role: '测试', online: false, rank: 1 }, // 无座位（离线配置/离席记忆）
];

// 装一个就绪态的花名册：fetch 顺序＝getPeople → getEstablishment → getAgents
async function viewWith(people = PEOPLE, archived = []) {
  const { El } = dom();
  fetchStub([people, { rows: [] }, archived]);
  const { RosterView } = await import('./list.js');
  const pane = new El('div');
  const view = new RosterView(pane, board());
  await view.reload();
  return { view, pane };
}

// 抠出一名成员的整行 HTML（data-person 起、</button> 止）
function rowOf(html, name) {
  const m = html.match(new RegExp(`data-person="${name}"[\\s\\S]*?</button>`));
  assert.ok(m, `${name} 的行应在名册里`);
  return m[0];
}

test('行内项目列：Niuma_Studio/项目名/未入座各有其貌', async () => {
  const { pane } = await viewWith();
  const html = pane.innerHTML;
  assert.match(rowOf(html, '阿甲'), /class="pproj"[^>]*>Niuma_Studio</);
  assert.match(rowOf(html, '小乙'), /class="pproj"[^>]*>甲项目</);
  assert.match(rowOf(html, '老丙'), /class="pproj none"[^>]*>—</);
  assert.ok(!html.includes('tag room'), '旧的 @房间 名标已由项目列接管');
});

test('工具条：项目下拉（全部/Niuma_Studio/项目/未入座）＋搜索框', async () => {
  const { pane } = await viewWith();
  const html = pane.innerHTML;
  assert.ok(html.includes('data-projects'), '项目下拉在');
  for (const label of ['全部项目', 'Niuma_Studio', '甲项目', '未入座']) {
    assert.ok(html.includes(`>${label}</option>`), `「${label}」选项在`);
  }
  assert.ok(html.includes('data-q') && html.includes('搜索姓名 / 身份'), '搜索框在');
});

test('项目筛选：按房间键过滤，未入座兜住无座成员', async () => {
  const { view, pane } = await viewWith();
  view.projectFilter = 'alpha';
  view.render();
  assert.ok(pane.innerHTML.includes('data-person="小乙"'));
  assert.ok(!pane.innerHTML.includes('data-person="阿甲"'));
  assert.ok(!pane.innerHTML.includes('data-person="老丙"'));
  view.projectFilter = 'none';
  view.render();
  assert.ok(pane.innerHTML.includes('data-person="老丙"'));
  assert.ok(!pane.innerHTML.includes('data-person="小乙"'));
});

test('搜索：姓名/身份子串、大小写不敏感，与项目筛选可叠加', async () => {
  const { view, pane } = await viewWith();
  view.q = '测试';
  view.render();
  assert.ok(pane.innerHTML.includes('data-person="老丙"'));
  assert.ok(!pane.innerHTML.includes('data-person="小乙"'));
  view.q = 'backend'; // 命中小乙的身份 Backend Dev
  view.render();
  assert.ok(pane.innerHTML.includes('data-person="小乙"'));
  assert.ok(!pane.innerHTML.includes('data-person="老丙"'));
  view.q = '小'; // 叠加项目过滤后：小乙在 alpha，被 'default' 筛掉
  view.projectFilter = 'default';
  view.render();
  assert.ok(!pane.innerHTML.includes('data-person="小乙"'));
  assert.ok(pane.innerHTML.includes('没有匹配的成员'));
});

test('归档页签：项目下拉禁用，搜索仍生效', async () => {
  const { view, pane } = await viewWith(PEOPLE, [
    { name: '旧人', role: '开发' },
    { name: '老古董', role: '测试' },
  ]);
  view.filter = 'archived';
  view.q = '古董';
  view.render();
  const html = pane.innerHTML;
  assert.match(html, /<select[^>]*data-projects[^>]*disabled/, '归档视图项目下拉禁用');
  assert.ok(html.includes('data-person="老古董"'));
  assert.ok(!html.includes('data-person="旧人"'));
});

test('项目筛到空：出生 CTA 预填该项目（新开张项目的第一步）', async () => {
  const { view, pane } = await viewWith();
  view.projectFilter = 'beta'; // active 名册里无人坐的项目
  view.render();
  const html = pane.innerHTML;
  assert.ok(html.includes('「乙项目」还没有成员'), '空项目空态卡在');
  assert.ok(html.includes('data-cta-recruit-project="beta"'), 'CTA 带项目键');
  // 未入座筛空（叠加搜索后无人满足）不出 CTA——那不是出生能解的空
  view.projectFilter = 'none';
  view.q = '小乙'; // 小乙坐在 alpha，被「未入座」筛掉
  view.render();
  assert.ok(pane.innerHTML.includes('没有匹配的成员'), '未入座空态走普通提示');
});

test('空工作室：空态卡照旧（工具条不抢出生 CTA）', async () => {
  const { pane } = await viewWith([]);
  const html = pane.innerHTML;
  assert.ok(html.includes('办公室里还没有人'), '空态卡在');
  assert.ok(html.includes('data-cta-recruit'), '出生 CTA 在');
  assert.ok(html.includes('data-q'), '工具条在（无行可筛也不断梁）');
});
