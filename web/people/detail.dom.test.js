// detail.dom.test.js — 人物详情侧栏「项目跟人走」的 DOM 冒烟（testdom
// 第一层）。口径（对不上字的两个根因都在这层）：
//   ① 能力装配的席位默认 = 本人所在项目（PersonSummary.room，座位真源）
//     ——旧写法在 #cross 落地前读 dispatch（换人刚清空），每个人物详情
//     的席位都先落「大厅」再粘死；
//   ② 手动挑位只在本人的视图生命周期内存活，换人即弃置；
//   ③ 头部「受管」标签显示项目名（大厅/项目名），与花名册项目列同口径
//     ——不再裸 key（受管 · default / 受管 · alpha）；
//   ④ 改档两槽选择即提交（「按了就要切换」）：模型/强度下拉一变即走
//     /dispatch/model 写面，确认钮退役；强度单独挑即交，模型槽原样
//     带上＝只动强度不动模型。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom, El } from '../room/testdom.js';

const CHOICES = [
  { key: 'default', name: 'Niuma_Studio' },
  { key: 'alpha', name: '甲项目' },
];
const PEOPLE = {
  小乙: { name: '小乙', role: 'Backend Dev', online: true, room: 'alpha', rank: 2 },
  阿甲: { name: '阿甲', role: '编排者', online: true, room: 'default', rank: 3 },
};
// 每个项目的 /dispatch 快照（#cross 的受管判定源）
const DISPATCH = {
  '': [{ name: '阿甲', status: 'idle', queued: 0, inject: 0 }],
  alpha: [{ name: '小乙', status: 'running', queued: 0, inject: 0 }],
};

function resp(body) {
  return {
    ok: true, status: 200,
    headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
    json: async () => body,
    // postText 的读面（/dispatch/model 等写面动词喝 text，不喝 json）
    text: async () => JSON.stringify(body),
  };
}

// 按端点回 JSON：/kb/people/{name}（详情）、…/history、/kb/tasks?assignee、
// /dispatch 系列（models 必须先于 project/裸 dispatch 判定——都含
// /dispatch 前缀）、/p/{key}/staffing/{person}/fabric；其余空对象。
// calls 记 {url, body}（POST 写面的载荷即交断言喝 body）。
function stubAPI() {
  const calls = [];
  globalThis.fetch = async (url, init) => {
    const u = String(url);
    calls.push({ url: u, body: init?.body });
    if (/\/kb\/people\/[^/]+\/history$/.test(u)) return resp({ name: '', lines: [] });
    if (/\/kb\/people\/[^/]+$/.test(u)) {
      const name = decodeURIComponent(u.split('/kb/people/')[1]);
      return resp(PEOPLE[name] || { name, rank: 1 });
    }
    if (u.includes('/kb/tasks')) return resp([]);
    if (u.includes('/dispatch/models')) return resp({ default: 'm', providers: [], levels: {} });
    if (u.includes('/dispatch?project=')) {
      return resp(DISPATCH[new URLSearchParams(u.split('?')[1]).get('project')] || []);
    }
    if (/^\/dispatch\/?$/.test(u)) return resp(DISPATCH['']);
    if (/\/staffing\/[^/]+\/fabric$/.test(u)) {
      return resp({ project: '', person: '', skills: [], mcps: [], fabric: {} });
    }
    return resp({});
  };
  return calls;
}

// board 替身：DetailView 只喝 projectChoices/toast/requestRefresh/
// selected/roster.render/ownerSend
function fakeBoard() {
  return {
    projectChoices: () => CHOICES,
    toast: { show() {} },
    requestRefresh() {},
    selected: null,
    roster: { render() {} },
    ownerSend: () => true,
  };
}

async function detailView() {
  const d = dom();
  const calls = stubAPI();
  const { DetailView } = await import('./detail.js');
  const side = new El('aside');
  return { detail: new DetailView(side, fakeBoard()), side, calls, ...d };
}

// 轮询等待异步面落位（cross/fabric 都在 reload 返回后仍在飞）
async function until(cond, label, ms = 1000) {
  const t0 = Date.now();
  while (!cond()) {
    if (Date.now() - t0 > ms) assert.fail(`等待超时：${label}`);
    await new Promise((r) => setTimeout(r, 5));
  }
}

test('席位默认跟人走：小乙在甲项目，能力装配落在甲项目而不是 Niuma_Studio', async () => {
  const { detail, side } = await detailView();
  detail.reload('小乙');
  await until(() => detail.fabric.state === 'ready', 'fabric ready');
  assert.equal(detail.fabric.project, 'alpha', '席位默认 = 本人的 room');
  assert.match(side.innerHTML, /value="alpha" selected/, '席位下拉选中甲项目');
  detail.board.selected = null;
  resetDom();
});

test('换人弃置挑位：上一人的席位选择不带到下一人', async () => {
  const { detail, side } = await detailView();
  detail.reload('小乙');
  await until(() => detail.fabric.state === 'ready', '小乙 fabric ready');
  // 模拟操作员在 小乙 的视图里手动挑了大厅（change 路由 testdom 不模拟，
  // 直接置位——picked 只该由 change 处理器置 true）
  detail.fabric.picked = true;
  detail.fabric.project = 'default';
  await detail.reload('阿甲'); // 换人：resetPick 生效（await 到 fabric.load 已同步跑完）
  await until(() => detail.fabric.state === 'ready', '阿甲 ready');
  assert.equal(detail.fabric.project, 'default', '阿甲本就在 Niuma_Studio');
  assert.equal(detail.fabric.picked, false, '上一人的手动挑位已弃置');
  // 再换回 小乙：席位重新默认到甲项目（不是上一视图的遗留挑位）
  await detail.reload('小乙'); // await：否则轮询会命中阿甲遗留的 ready 态
  await until(() => detail.fabric.state === 'ready', '小乙二次 ready');
  assert.equal(detail.fabric.project, 'alpha', '换回小乙，席位跟回甲项目');
  resetDom();
});

test('手动挑位在本人视图内存活：刷新周期不顶掉', async () => {
  const { detail } = await detailView();
  detail.reload('小乙');
  await until(() => detail.fabric.state === 'ready', '小乙 fabric ready');
  detail.fabric.picked = true;
  detail.fabric.project = 'default'; // 手动挑大厅预览
  await detail.reload('小乙'); // 同人刷新（30s 轮询/房间事件的常态）
  await until(() => detail.fabric.state === 'ready', '刷新后 ready');
  assert.equal(detail.fabric.project, 'default', '本人视图内挑位存活');
  resetDom();
});

test('受管标签显示项目名：与花名册项目列同口径，不裸 key', async () => {
  const { detail, side } = await detailView();
  detail.reload('小乙');
  await until(() => side.innerHTML.includes('受管 ·'), 'cross 落地');
  assert.ok(side.innerHTML.includes('受管 · 甲项目'), '显示项目名「甲项目」');
  assert.ok(!side.innerHTML.includes('受管 · alpha'), '不显示裸 key');
  assert.ok(side.innerHTML.includes('title="alpha"'), '裸 key 留在 tooltip 里可溯源');
  resetDom();
});

// ④ 改档两槽选择即提交。onChange 是 change 事件的公共缝（composer
// 同款——testdom 不模拟事件路由，用例直调）；target 用带 matches 的
// 简单对象替身，value 就是挑的档。
test('模型一选即走写面：POST /dispatch/model 即发，确认钮退役', async () => {
  const { detail, side, calls } = await detailView();
  detail.reload('小乙');
  await until(() => side.innerHTML.includes('受管 ·'), 'cross 落地');
  assert.ok(!side.innerHTML.includes('data-model-set'), '改档确认钮已退役');
  detail.onChange({ target: { value: 'prov/GLM-Y', matches: (s) => s === '[data-model]' } });
  await until(() => calls.some((c) => c.url === '/dispatch/model'), 'POST 已发');
  const posted = JSON.parse(calls.find((c) => c.url === '/dispatch/model').body);
  assert.equal(posted.name, '小乙');
  assert.equal(posted.model, 'prov/GLM-Y');
  assert.equal(posted.project, 'alpha', '受管成员带所在项目写');
  resetDom();
});

test('强度单独挑即交：模型槽原样带上，只动强度不动模型', async () => {
  const { detail, side, calls } = await detailView();
  // 稳定的下拉替身：querySelector 恒回同一对节点，模型下拉摆出座位
  // 现档——强度挑完提交，模型槽必须是这把原值，而不是被清空
  const modelSel = new El('select'); modelSel.value = 'prov/GLM-X';
  const lvlSel = new El('select');
  side.querySelector = (sel) =>
    (sel === '[data-model]' ? modelSel : sel === '[data-reasoning]' ? lvlSel : new El('div'));
  detail.reload('小乙');
  await until(() => side.innerHTML.includes('受管 ·'), 'cross 落地');
  detail.onChange({ target: { value: 'high', matches: (s) => s === '[data-reasoning]' } });
  await until(() => calls.some((c) => c.url === '/dispatch/model'), 'POST 已发');
  const posted = JSON.parse(calls.find((c) => c.url === '/dispatch/model').body);
  assert.equal(posted.model, 'prov/GLM-X', '模型槽原样带上（不动模型）');
  assert.equal(posted.reasoning, 'high');
  resetDom();
});
