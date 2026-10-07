// achievements.dom.test.js — 成就页签（像素收藏品货架面）的 DOM 冒烟。
// 数据面＝t_187 引擎的真实载荷：定义清单（含 medal 定档 0金1银2铜）＋
// 解锁键集（"key" 全室集体 / "key@成员" 个人奖章——面板从键反解 holders）。
// 展出面契约：两分区（perMember 切）＋每排 6 展位不足补空底座＋未解锁
// 剪影暗格＋柜底详情条（默认最近达成，select() 换详情）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from '../room/testdom.js';

const PAYLOAD = {
  unlocked: { 'first@小狐': 1, 'ten@小猿': 2, 'ten@小狐': 3, 'studio-x': 4 },
  achievements: [
    { key: 'first', name: '首单交付', text: '交付了第一张任务单', perMember: true, medal: 2 },
    { key: 'ten', name: '累计十单', text: '累计交付了十张任务单', perMember: true, medal: 1 },
    { key: 'studio-x', name: '测试百项', text: '全量测试过百', perMember: false, medal: 0 },
    { key: 'locked-one', name: '成就大满贯', text: '集齐所有奖章', perMember: false, medal: 0 }, // 未解锁
  ],
};

test('achievements 货架：两分区＋空位补底座＋剪影暗格＋定档配色＋默认详情', async () => {
  dom();
  fetchStub([PAYLOAD]);
  const { AchievementsPanel } = await import('./achievements.js');
  const p = new AchievementsPanel();
  const root = new El('div');
  await p.load(root);
  assert.equal(p.state, 'ready');
  const html = root.innerHTML;
  assert.ok(html.includes('trophy-case'), '柜面在');
  assert.ok(html.includes('个人里程碑'), '个人分区在');
  assert.ok(html.includes('集体记忆'), '集体分区在');
  assert.ok(html.includes('预留展位'), '预留展位排在');
  assert.ok(html.includes('已达成 3 / 4 枚'), '柜头计数对');
  // 展位：4 定义各一位，两排各补足 6 位（4 空底座）＋预留排 6 空位
  assert.equal(html.split('data-ach="').length - 1, 4, '定义展位数');
  assert.equal(html.split('tc-pegbar').length - 1, 14, '空位/预留展位补底座');
  assert.equal(html.split('tc-svg').length - 1, 4, '每展位一只奖杯精灵');
  assert.ok(html.includes('crispEdges'), '像素棱角钉死');
  // 未解锁：剪影暗格（tc-todo）；已解锁：名字铭牌
  assert.ok(html.includes('tc-todo'), '未解锁暗格在');
  // 定档：first 铜（#e0a03a）/ ten 银（#c8cfd6）
  assert.ok(html.includes('#e0a03a'), '铜档取色在');
  assert.ok(html.includes('#c8cfd6'), '银档取色在');
  // 默认详情＝最近达成（studio-x ts=4，全室集体挂「工作室」）
  assert.equal(p.sel, 'studio-x', '默认选中最近达成');
  assert.ok(html.includes('工作室'), '集体成就挂工作室名');
  assert.ok(html.includes('金章'), '档位章级在');
  assert.ok(!html.includes('undefined'), '无渲染残渣');
  resetDom();
});

test('achievements 详情条：@成员反解挂名＋select() 换详情', async () => {
  dom();
  fetchStub([PAYLOAD]);
  const { AchievementsPanel } = await import('./achievements.js');
  const p = new AchievementsPanel();
  await p.load(new El('div'));
  // 个人奖章 ten：解锁键 ten@小猿/ten@小狐 → holders 小猿、小狐，银章
  const ten = p.detailHtml(p.data.find((a) => a.key === 'ten'));
  assert.ok(ten.includes('小猿、小狐'), '个人奖章挂名在');
  assert.ok(ten.includes('银章'), '章级对');
  assert.ok(ten.includes('累计交付了十张任务单'), '文案在');
  // select() 切到未解锁：详情给目标预览（不装已达成的章级）
  p.select('locked-one');
  assert.equal(p.sel, 'locked-one', '选中态换位');
  const locked = p.detailHtml(p.data.find((a) => a.key === 'locked-one'));
  assert.ok(locked.includes('未解锁'), '未解锁章级在');
  assert.ok(!locked.includes('达成于'), '未解锁不给达成时间');
  resetDom();
});

test('achievements：空清单降级＋fetch 失败重试钮（不白屏）', async () => {
  dom();
  fetchStub([{ unlocked: {}, achievements: [] }]);
  const { AchievementsPanel } = await import('./achievements.js');
  let p = new AchievementsPanel();
  let root = new El('div');
  await p.load(root);
  assert.equal(p.state, 'empty');
  assert.ok(root.innerHTML.includes('成就清单还没定稿'), '空态提示在');

  fetchStub([{ ok: false, status: 500, json: { error: 'x' } }]);
  p = new AchievementsPanel();
  root = new El('div');
  await p.load(root);
  assert.equal(p.state, 'error');
  assert.ok(root.innerHTML.includes('chron-retry'), '重试钮在');
  resetDom();
});
