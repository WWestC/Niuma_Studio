// gitgraph.test.js — 提交图布局契约（node --test，零 DOM）：泳道分配
// 的五类拓扑形状（线性/分叉/汇合/merge 分叉/车道回收复用）＋截断线与
// 根提交的边界＋refs 贴行排序。改布局算法前后必跑（node --test
// web/project/gitgraph.test.js）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { layoutGraph, refsBySHA, edgePath, ROW_H, LANE_X0, LANE_W } from './gitgraph.js';

const C = (sha, ...parents) => ({ sha, parent_shas: parents, parents: parents.length });
const lanes = (g, ...shas) => shas.map((s) => g.laneOf.get(s));

test('线性历史：一条道走到底，根提交无线下延', () => {
  const g = layoutGraph([C('c3', 'c2'), C('c2', 'c1'), C('c1')]);
  assert.deepEqual(lanes(g, 'c3', 'c2', 'c1'), [0, 0, 0]);
  assert.equal(g.maxLane, 0);
  assert.equal(g.edges.length, 2); // c3→c2、c2→c1；c1 是根无下延
  assert.ok(g.edges.every((e) => e.fromLane === 0 && e.toLane === 0));
});

test('分叉后汇合：两道并行一段，共祖处并回一道', () => {
  // 两条分支头同指一父：并行两道，在共祖行汇成一道
  const g = layoutGraph([C('m1', 'base'), C('s1', 'base'), C('base')]);
  const [lm, ls, lb] = lanes(g, 'm1', 's1', 'base');
  assert.notEqual(lm, ls);
  assert.ok(lb === lm || lb === ls);
  // 汇入弧线存在：一条边跨道（fromLane ≠ toLane）
  const cross = g.edges.filter((e) => e.fromLane !== e.toLane);
  assert.equal(cross.length, 1);
  assert.equal(cross[0].toLane, lb);
});

test('merge 提交：本道延续 parent[0]，parent[1] 开新道向下分叉', () => {
  const g = layoutGraph([C('m', 's2', 'side'), C('s2', 'base'), C('side', 'base'), C('base')]);
  const [lm, ls2, lside, lbase] = lanes(g, 'm', 's2', 'side', 'base');
  assert.equal(lm, ls2);           // 主线延续
  assert.notEqual(lside, ls2);     // side 独立道
  assert.equal(g.maxLane, 1);
  // m→side 边从 m 的道弯向 side 的道
  const fork = g.edges.find((e) => e.fromIdx === 0 && e.toIdx === 2);
  assert.ok(fork);
  assert.equal(fork.fromLane, lm);
  assert.equal(fork.toLane, lside);
  // base 只落一道，另一道在 base 处汇入
  assert.ok(lbase === ls2 || lbase === lside);
});

test('车道回收复用：汇合腾出的道被后续分叉复用，图不横向膨胀', () => {
  // 分叉 m1/s1 → 汇合 base → base 之下再无分叉；随后新头 n1 复用空道
  const g = layoutGraph([C('m1', 'base'), C('s1', 'base'), C('base', 'old'), C('n1', 'old2'), C('old')]);
  // n1 是窗口内的独立头（old2 不在窗口），应复用已释放的道而不是开第三道
  assert.equal(g.maxLane, 1);
  const ln = g.laneOf.get('n1');
  assert.ok(ln === 0 || ln === 1);
});

test('窗口截断：父在窗口外画淡出线（toIdx=-1），根提交不画', () => {
  const g = layoutGraph([C('c2', 'ghost'), C('c1')]);
  const cut = g.edges.filter((e) => e.toIdx === -1);
  assert.equal(cut.length, 1);      // c2 → ghost
  assert.equal(cut[0].fromLane, 0);
  assert.equal(g.edges.length, 1);  // c1 是根：无边
});

test('edges 与窗口内父子关系一一对应', () => {
  const commits = [C('a', 'b', 'x'), C('b', 'c'), C('x', 'c'), C('c')];
  const g = layoutGraph(commits);
  assert.equal(g.edges.length, 4);  // a→b、a→x、b→c、x→c
  const pairs = g.edges.map((e) => `${e.fromIdx}>${e.toIdx}`).sort();
  assert.deepEqual(pairs, ['0>1', '0>2', '1>3', '2>3']);
});

test('edgePath：同道是竖线，跨道是贝塞尔弧，坐标按行序展开', () => {
  const straight = edgePath({ fromIdx: 0, toIdx: 1, fromLane: 0, toLane: 0 });
  assert.equal(straight, `M${LANE_X0} ${ROW_H / 2}L${LANE_X0} ${ROW_H + ROW_H / 2}`);
  const arc = edgePath({ fromIdx: 0, toIdx: 2, fromLane: 0, toLane: 1 });
  assert.ok(arc.startsWith(`M${LANE_X0} ${ROW_H / 2}C`));
  assert.ok(arc.includes(`${LANE_X0 + LANE_W} `));
  // 截断线只向下多走一行
  const cut = edgePath({ fromIdx: 3, toIdx: -1, fromLane: 2, toLane: 2 });
  assert.ok(cut.endsWith(`L${LANE_X0 + 2 * LANE_W} ${4 * ROW_H + ROW_H / 2}`));
});

test('refsBySHA：分支徽章按 sha 贴行，当前分支排最前', () => {
  const by = refsBySHA([
    { name: 'main', sha: 's1', current: true },
    { name: 'origin/main', sha: 's1', remote: 'origin' },
    { name: 'feat-x', sha: 's2' },
  ]);
  assert.deepEqual(by.get('s1').map((r) => r.name), ['main', 'origin/main']);
  assert.deepEqual(by.get('s2').map((r) => r.name), ['feat-x']);
  assert.ok(!by.has('s0'));
  assert.deepEqual(refsBySHA(null).size, 0); // nil 安全
});
