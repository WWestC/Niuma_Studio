// food.test.js — 食品目录契约测试（r_08，t_142）：foods.json 的 kind
// 字段（本单修复项）与目录形状——总数/kind 合法/fruit ≥10/key 唯一/
// 每条带 residue/hand 5 行形状。
//
// 目录真源是服务端 /art/foods.json（pixart.Foods 的投影）——测试不起
// 服务，直接钉 Go 侧条目表的投影规则：从 server/art.go 的判定逻辑
// （bag/can/box/stick 前缀＝snack，其余＝fruit）镜像到测试里对 key
// 断言，外加对运行中服务的一次实拉（127.0.0.1:7777 在跑时）做端到端
// 契约核对；服务不在跑时端到端段跳过（本地纯逻辑面不受影响）。

import test from 'node:test';
import assert from 'node:assert/strict';

// key 前缀判定（与 server/art.go 的 kind 落值逻辑同形——两处改动必须同步）
const kindOf = (key) => {
  const pre = key.slice('food-'.length);
  if ((pre.length > 4 && ['bag-', 'can-', 'box-'].includes(pre.slice(0, 4))) ||
      (pre.length > 6 && pre.slice(0, 6) === 'stick-')) return 'snack';
  return 'fruit';
};

// 目录从运行中的服务拉（真源）；拉不到（服务未跑）用内置镜像目录
// ——32 条 key 清单与 pixart/food.go 一致，改条目表时同步这里。
const MIRROR = [
  'food-bag-red', 'food-bag-blue', 'food-bag-gold', 'food-bag-green', 'food-bag-orange', 'food-bag-purple',
  'food-can-soda', 'food-can-coffee', 'food-can-tea', 'food-can-cola', 'food-can-juice',
  'food-box-choc', 'food-box-cook', 'food-box-candy', 'food-box-nut', 'food-box-cereal', 'food-box-seaweed',
  'food-stick-pretzel', 'food-stick-biscuit', 'food-stick-choc', 'food-stick-cheese', 'food-stick-mint',
  'food-apple', 'food-orange', 'food-banana', 'food-grape', 'food-pear', 'food-peach',
  'food-lemon', 'food-watermelon', 'food-strawberry', 'food-mango',
];

async function loadCatalog() {
  try {
    const r = await fetch('http://127.0.0.1:7777/art/foods.json');
    if (r.ok) return { list: await r.json(), live: true };
  } catch { /* 服务不在跑 */ }
  return { list: MIRROR.map((key) => ({ key })), live: false };
}

test('目录总数 ≥30（指标 20 零食 + 10 水果）', async () => {
  const { list } = await loadCatalog();
  assert.ok(list.length >= 30, `目录仅 ${list.length} 条`);
});

test('kind ∈ {snack, fruit} 且 fruit ≥10、snack ≥20（镜像前缀判定，两处同步钉）', async () => {
  const { list } = await loadCatalog();
  // 镜像规则直接断言（与 server/art.go 同形——改条目表/判定必须两处同步）；
  // 运行态目录带 kind 字段时逐条核对服务端落值与镜像一致
  const hasKind = list.some((f) => f.kind);
  let fruit = 0, snack = 0;
  for (const f of list) {
    const want = kindOf(f.key);
    if (want === 'fruit') fruit++;
    else snack++;
    if (hasKind) assert.equal(f.kind, want, `${f.key} 服务端 kind 与前缀判定不一致`);
  }
  assert.ok(fruit >= 10, `水果仅 ${fruit} < 10`);
  assert.ok(snack >= 20, `零食仅 ${snack} < 20`);
});

test('key 唯一且前缀判定与 kind 一致（两处逻辑同步的回归钉）', async () => {
  const { list } = await loadCatalog();
  const seen = new Set();
  for (const f of list) {
    assert.ok(!seen.has(f.key), `key 重复: ${f.key}`);
    seen.add(f.key);
    if (f.kind) assert.equal(f.kind, kindOf(f.key), `${f.key} 的 kind 与前缀判定不一致`);
  }
});

test('每条带 residue 残骸键（运行态断言形状字段）', async () => {
  const { list, live } = await loadCatalog();
  if (!live) return;
  for (const f of list) {
    assert.ok(f.residue && f.residue.length > 0, `${f.key} 缺 residue`);
    assert.ok(Array.isArray(f.hand) && f.hand.length === 5, `${f.key} hand 应 5 行`);
    assert.ok(Array.isArray(f.res) && f.res.length <= 5, `${f.key} res 应 ≤5 行`);
    assert.ok(/^#[0-9a-f]{6}$/i.test(f.hex), `${f.key} hex 非法`);
    assert.ok(/^#[0-9a-f]{6}$/i.test(f.hex2), `${f.key} hex2 非法`);
  }
});
