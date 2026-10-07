// members.dom.test.js — 侧栏名册「干活中」chip 的冒烟面（v2.10 同步
// 收口）：live member_work 翻转（roster 行自带的 working）不得因
// /kb/people 未应答（state null 的冷窗）而被压掉；冷源 people 表里的
// working 照样点亮；离线门照旧拦截。chip 的 data-trace 指名可开抽屉。
import test from 'node:test';
import assert from 'node:assert';
import { dom } from '../room/testdom.js';

dom();

const { MembersView } = await import('./members.js');

function stage(rows, peopleRows) {
  const room = {
    rosterLoaded: true,
    members: new Map(rows.map((r) => [r.name, r])),
    queues: new Map(),
  };
  const book = { currentRoom: () => room, sidebarRoster: () => rows };
  const list = document.createElement('div');
  const head = document.createElement('div');
  const mv = new MembersView(list, book, { head });
  if (peopleRows) {
    mv.people = new Map(peopleRows.map((p) => [p.name, p]));
    mv.fetchedAt = Date.now();
  }
  mv.render();
  return { mv, list, head };
}

test('live working 翻转不受 /kb/people 冷窗压制（state null 也亮 chip）', () => {
  const { list } = stage([{ name: '小策', role: '前端', working: true, working_since: 1 }]);
  const html = list.children[0].innerHTML;
  assert.ok(html.includes('干活中'), 'chip 必须渲染');
  assert.ok(html.includes('data-trace="小策"'), 'chip 必须指名可开抽屉');
});

test('冷源 working（/kb/people 回灌）同样点亮 chip', () => {
  const { list } = stage(
    [{ name: '小策', role: '前端' }],
    [{ name: '小策', online: true, working: true }],
  );
  assert.ok(list.children[0].innerHTML.includes('干活中'), '冷源 working 该亮 chip');
});

test('离线门照旧：people 说离线时 chip 不亮', () => {
  const { list } = stage(
    [{ name: '小策', role: '前端' }],
    [{ name: '小策', online: false, working: true }],
  );
  assert.ok(!list.children[0].innerHTML.includes('干活中'), '离线时不该亮 chip');
});

test('闲成员不亮 chip，头计数照画', () => {
  const { list, head } = stage(
    [{ name: '小策', role: '前端' }, { name: '小鹿', role: '设计', working: true, working_since: 1 }],
    [{ name: '小策', online: true }],
  );
  assert.ok(!list.children[0].innerHTML.includes('干活中'), '闲人不亮 chip');
  assert.ok(list.children[1].innerHTML.includes('干活中'), '忙人亮 chip');
  assert.ok(head.textContent.includes('2'), '在线计数 = 2');
});
