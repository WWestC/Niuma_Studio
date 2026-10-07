// reqs.test.js — 需求台账排序＋过滤＋删除钮契约（node --test，零
// DOM）：四档全序（创建/完成 × 新旧两向，默认创建最新）＋未完成恒
// 沉底＋沉底内部仍按创建最新＋迁移形状（closed 但没盖到 closed_ts
// 的旧档沉底）＋纯函数不改入参＋排序键白名单化；过滤面 filterReqs
// ——关键词命中 id/标题/正文/录入人（大小写不敏感、空白自剥）＋状
// 态档叠加＋空档即全量；删除钮 deleteBtnHTML——仅 open 行两态（常态/
// 武装态），split/closed 无钮。改排序/筛选/删除前后必跑：
//   node --test web/project/reqs.test.js

import test from 'node:test';
import assert from 'node:assert/strict';
import { sortReqs, normalizeSort, filterReqs, deleteBtnHTML } from './reqs.js';
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


const R = (id, created_ts, closed_ts = 0, status = 'open') =>
  ({ id, created_ts, closed_ts, status });

// 一桌混席：三条已关闭（完成时刻乱序）、一条 split、两条 open——
// 沉底、并排、迁移形状都在里面
const LEDGER = [
  R('r_01', 100, 500, 'closed'),
  R('r_02', 200, 0, 'open'),
  R('r_03', 300, 700, 'closed'),
  R('r_04', 400, 0, 'split'),
  R('r_05', 600, 200, 'closed'),
  R('r_06', 800, 0, 'open'),
];

const ids = (list) => list.map((r) => r.id);

test('默认档创建最新：created_ts 降序', () => {
  assert.deepEqual(ids(sortReqs(LEDGER, 'created_desc')), ['r_06', 'r_05', 'r_04', 'r_03', 'r_02', 'r_01']);
});

test('创建最早：created_ts 升序', () => {
  assert.deepEqual(ids(sortReqs(LEDGER, 'created_asc')), ['r_01', 'r_02', 'r_03', 'r_04', 'r_05', 'r_06']);
});

test('完成最新：有完成时刻的降序在前，未完成恒沉底', () => {
  // r_03(700) > r_01(500) > r_05(200)；r_02/r_04/r_06 沉底
  assert.deepEqual(ids(sortReqs(LEDGER, 'closed_desc')),
    ['r_03', 'r_01', 'r_05', 'r_06', 'r_04', 'r_02']);
});

test('完成最早：有完成时刻的升序在前，沉底方向不翻', () => {
  // 降序换升序只翻有戳区；未完成的照样沉底（board.js 无截止沉底同规）
  assert.deepEqual(ids(sortReqs(LEDGER, 'closed_asc')),
    ['r_05', 'r_01', 'r_03', 'r_06', 'r_04', 'r_02']);
});

test('沉底内部仍按创建最新：形状稳定不跳', () => {
  const only = [R('r_01', 100), R('r_02', 300), R('r_03', 200)];
  assert.deepEqual(ids(sortReqs(only, 'closed_desc')), ['r_02', 'r_03', 'r_01']);
  assert.deepEqual(ids(sortReqs(only, 'closed_asc')), ['r_02', 'r_03', 'r_01']);
});

test('迁移形状：closed 状态但没盖到 closed_ts 的旧档也沉底', () => {
  const legacy = [R('r_01', 100, 900, 'closed'), R('r_09', 50, 0, 'closed')];
  assert.deepEqual(ids(sortReqs(legacy, 'closed_desc')), ['r_01', 'r_09']);
  assert.deepEqual(ids(sortReqs(legacy, 'closed_asc')), ['r_01', 'r_09']);
});

test('纯函数：不改入参数组（this.reqs 缓存保持插入序）', () => {
  const src = [R('r_02', 200), R('r_01', 100)];
  sortReqs(src, 'created_desc');
  assert.deepEqual(ids(src), ['r_02', 'r_01']);
});

test('排序键白名单化：未知键（老存档/手改）回落默认档创建最新', () => {
  assert.equal(normalizeSort('created_desc'), 'created_desc');
  assert.equal(normalizeSort('closed_asc'), 'closed_asc');
  assert.equal(normalizeSort('bogus'), 'created_desc');
  assert.equal(normalizeSort(undefined), 'created_desc');
  // 白名单兜底与显式默认同物：sortReqs 对未知键也走 created_desc
  assert.deepEqual(ids(sortReqs(LEDGER, 'bogus')), ids(sortReqs(LEDGER, 'created_desc')));
});

// --- 过滤面 filterReqs -------------------------------------------------------

const RICH = [
  { id: 'r_01', title: '登录页改版', body: '支持手机号验证码', created_by: '小狐', status: 'open' },
  { id: 'r_02', title: '报表导出', body: 'Excel/CSV 双格式', created_by: '小牛', status: 'split' },
  { id: 'r_03', title: '登录限流', body: '同 IP 每分钟十次', created_by: '小狐', status: 'closed' },
];

test('关键词命中四个字段：id / 标题 / 正文 / 录入人', () => {
  assert.deepEqual(ids(filterReqs(RICH, 'r_02', '')), ['r_02']);       // id
  assert.deepEqual(ids(filterReqs(RICH, '导出', '')), ['r_02']);        // 标题
  assert.deepEqual(ids(filterReqs(RICH, '验证码', '')), ['r_01']);      // 正文
  assert.deepEqual(ids(filterReqs(RICH, '小狐', '')), ['r_01', 'r_03']); // 录入人
});

test('大小写不敏感＋两端空白自剥：英文关键词大小写、含空输入都能对上', () => {
  const mixed = [
    { id: 'r_01', title: 'CSV Export', body: '', status: 'open' },
    { id: 'r_02', title: '别的', body: '', status: 'open' },
  ];
  assert.deepEqual(ids(filterReqs(mixed, 'csv', '')), ['r_01']);
  assert.deepEqual(ids(filterReqs(mixed, '  export  ', '')), ['r_01']);
  assert.deepEqual(ids(filterReqs(mixed, '   ', '')), ['r_01', 'r_02']); // 纯空白＝无词
  assert.deepEqual(ids(filterReqs(mixed, '', '')), ['r_01', 'r_02']);    // 空词＝全量
});

test('状态档：open/split/closed 各取各的，空档即全量', () => {
  assert.deepEqual(ids(filterReqs(RICH, '', 'open')), ['r_01']);
  assert.deepEqual(ids(filterReqs(RICH, '', 'split')), ['r_02']);
  assert.deepEqual(ids(filterReqs(RICH, '', 'closed')), ['r_03']);
  assert.deepEqual(ids(filterReqs(RICH, '', '')), ['r_01', 'r_02', 'r_03']);
});

test('关键词×状态叠加：先筛状态再收词（与 #paint 的 filter→sort 同序）', () => {
  assert.deepEqual(ids(filterReqs(RICH, '登录', 'open')), ['r_01']);   // 词跨状态，档内才留
  assert.deepEqual(ids(filterReqs(RICH, '登录', 'closed')), ['r_03']);
  assert.deepEqual(ids(filterReqs(RICH, '登录', 'split')), []);        // 叠空＝「没有匹配的需求」
  // 与排序复合：过滤面喂排序面，链路各管各的
  assert.deepEqual(ids(sortReqs(filterReqs(RICH, '小狐', ''), 'created_desc')), ['r_01', 'r_03']);
});

test('纯函数：不改入参数组（this.reqs 缓存保持插入序）', () => {
  const src = [...RICH];
  filterReqs(src, '登录', 'open');
  assert.deepEqual(ids(src), ['r_01', 'r_02', 'r_03']);
});

test('删除钮：open 行常态钮带 data-del，武装行换「再点一次确认删除」红钮', () => {
  const open = R('r_02', 200);
  const idle = deleteBtnHTML(open, null);
  assert.ok(idle.includes('data-del="r_02"'), '常态钮应带 data-del');
  assert.ok(!idle.includes('danger'), '常态钮不是红钮');
  assert.ok(idle.includes('仅 open'), '常态钮 title 应点明仅 open 可删');

  const armed = deleteBtnHTML(open, 'r_02');
  assert.ok(armed.includes('danger'), '武装态应转红钮');
  assert.ok(armed.includes('再点一次确认删除'), '武装态文案固定');
  assert.ok(armed.includes('data-del="r_02"'), '武装态仍带同一 data-del（第二击即执行）');

  // 武装别行不影响本行：只有被点的那行换钮
  assert.ok(!deleteBtnHTML(open, 'r_06').includes('danger'));
});

test('删除钮：split/closed 行无钮（库拒删——UI 不给不能走的路）', () => {
  assert.equal(deleteBtnHTML(R('r_04', 400, 0, 'split'), null), '');
  assert.equal(deleteBtnHTML(R('r_04', 400, 0, 'split'), 'r_04'), '');
  assert.equal(deleteBtnHTML(R('r_01', 100, 500, 'closed'), null), '');
  assert.equal(deleteBtnHTML(R('r_01', 100, 500, 'closed'), 'r_01'), '');
});
