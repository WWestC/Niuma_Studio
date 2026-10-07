// recruit.dom.test.js — 出生表单「出生项目」缺省链的冒烟（testdom 第
// 一层：innerHTML 字符串面断言 selected 落点，不进提交/轮询路径）：
// 显式预填（编制页「招牛马」带目标项目）→ 房主当前正坐的房间 → 大
// 厅——人在哪个项目，出生就落在哪个项目；当前房不在可选面（未开张/
// 已归档/清单未及拉回）时照旧回大厅，不说谎。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom } from '../room/testdom.js';

const CHOICES = [
  { key: 'default', name: 'Niuma_Studio' },
  { key: 'alpha', name: '甲项目' },
  { key: 'beta', name: '乙项目' },
];

// board 替身：open() 只喝 projectChoices() 与 book.current；
// book 传 null 表示面板极早期（book 未及注入）。
function boardWith(current) {
  return { book: current === null ? undefined : { current }, projectChoices: () => CHOICES };
}

// 抠出唯一带 selected 的选项 key（全表单只有出生项目下拉会落 selected）
function pickedProject(html) {
  const keys = [...html.matchAll(/<option value="([^"]*)" selected>/g)].map((m) => m[1]);
  assert.equal(keys.length, 1, '恰好一个 selected 项目选项');
  return keys[0];
}

test('无预填：默认落在当前房间', async () => {
  dom();
  const { RecruitForm } = await import('./recruit.js');
  const form = new RecruitForm(null, boardWith('alpha'));
  form.open();
  assert.equal(pickedProject(form.el.innerHTML), 'alpha');
});

test('显式预填压过当前房间（编制页招牛马的目标项目优先）', async () => {
  dom();
  const { RecruitForm } = await import('./recruit.js');
  const form = new RecruitForm(null, boardWith('alpha'));
  form.open({ prefProject: 'beta' });
  assert.equal(pickedProject(form.el.innerHTML), 'beta');
});

test('预填失效时当前房间接管；当前房也不可选时回 Niuma_Studio', async () => {
  dom();
  const { RecruitForm } = await import('./recruit.js');
  const a = new RecruitForm(null, boardWith('beta'));
  a.open({ prefProject: 'ghost' });
  assert.equal(pickedProject(a.el.innerHTML), 'beta');
  const b = new RecruitForm(null, boardWith('ghost'));
  b.open();
  assert.equal(pickedProject(b.el.innerHTML), 'default');
});

test('面板极早期（book 未注入）：不炸，回 Niuma_Studio', async () => {
  dom();
  const { RecruitForm } = await import('./recruit.js');
  const form = new RecruitForm(null, boardWith(null));
  form.open();
  assert.equal(pickedProject(form.el.innerHTML), 'default');
});
