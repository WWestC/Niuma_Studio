// energy.test.js — 体力×零食×垃圾引擎的纯逻辑面（r_08，t_141）：
// EPARAMS 边界（防手滑改出死循环参数）与调参语义。

import test from 'node:test';
import assert from 'node:assert/strict';
import { EPARAMS, EnergyEngine } from './energy.js';

test('EPARAMS 十六键齐全（定稿 §六全量）', () => {
  const want = ['energyMax', 'drain', 'idleDrain', 'breakRegen', 'eatGain',
    'lowAt', 'critAt', 'hungryChance', 'snackCapacity', 'snackRestockAt',
    'binCapacity', 'binTossRange', 'litterDecay', 'litterMax', 'penalty', 'drama'];
  for (const k of want) assert.ok(k in EPARAMS, `缺参数 ${k}`);
});

test('数值边界：阈值有序、概率合法、容量为正', () => {
  assert.ok(EPARAMS.critAt < EPARAMS.lowAt, '饿过昏线必须低于低体力阈值');
  assert.ok(EPARAMS.lowAt < EPARAMS.energyMax, '低体力阈值必须低于上限');
  assert.ok(EPARAMS.hungryChance > 0 && EPARAMS.hungryChance <= 1, '概率 ∈ (0,1]');
  for (const k of ['energyMax', 'eatGain', 'breakRegen', 'snackCapacity',
    'binCapacity', 'litterDecay', 'litterMax']) {
    assert.ok(EPARAMS[k] > 0, `${k} 必须为正`);
  }
  assert.ok(EPARAMS.drain > 0 && EPARAMS.idleDrain > 0, '消耗为正（空闲也饿——慢漏）');
  assert.ok(EPARAMS.snackRestockAt < EPARAMS.snackCapacity, '补货线低于容量');
});

test('冷启动错峰：新成员初始体力落在 35–90（不都满值——开页半天没人开饭）', () => {
  const eng = new EnergyEngine(null); // en() 不碰 board，null 即可
  for (let i = 0; i < 60; i++) {
    const v = eng.en('m' + i);
    assert.ok(v >= 35 && v <= 90, `初始体力 ${v} 越界（应 35–90）`);
  }
  // 同名复取稳定（错峰只发生在首次落位）
  const first = eng.en('m0');
  assert.equal(eng.en('m0'), first, '同成员体力读数应稳定');
});

test('戏剧开关四条默认全关（定稿 §五）', () => {
  assert.equal(EPARAMS.drama.spill, false);
  assert.equal(EPARAMS.drama.hoard, false);
  assert.equal(EPARAMS.drama.critEat, false);
  assert.equal(EPARAMS.drama.fullBinToss, false);
});

test('调参语义：引用可变（改值即生效，无需重编译）', () => {
  const before = EPARAMS.penalty;
  EPARAMS.penalty = 3;
  assert.equal(EPARAMS.penalty, 3);
  EPARAMS.penalty = before; // 还原（模块级单例不污染其他测试）
});
