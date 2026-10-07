// social.test.js — 摸鱼社交机制 DSL 的纯逻辑面（r_08，t_141）：
// 12 套机制脚本结构合法（步数/时长/字段）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { MECHS, PARAMS, LINES } from './social.js';

test('机制清单十二套（硬指标 ≥10）', () => {
  const keys = Object.keys(MECHS);
  assert.ok(keys.length >= 10, `机制数 ${keys.length} < 10`);
  assert.equal(keys.length, 12, '定稿清单恰 12 套');
});

test('每套机制脚本 ≥3 步且每步 dur 非负', () => {
  for (const [key, m] of Object.entries(MECHS)) {
    assert.equal(typeof m.build, 'function', `${key}.build`);
    const steps = m.build({});
    assert.ok(steps.length >= 3, `${key} 脚本步数 ${steps.length} < 3`);
    for (const s of steps) {
      assert.ok(s.dur >= 0, `${key} 有负时长步`);
    }
  }
});

test('机制人数范围合法（min ≤ max ≥ 1）', () => {
  for (const [key, m] of Object.entries(MECHS)) {
    assert.ok(m.min >= 1 && m.max >= m.min, `${key} 人数范围 ${m.min}-${m.max} 非法`);
  }
});

test('M12 深夜限定与 M11 三人门槛的特殊标记', () => {
  assert.ok(MECHS['m12-nap'].opts.nightOnly, 'M12 深夜限定');
  // M11 的三人门槛在 beat() 里硬编码（team.length < 3 return）——
  // 这里钉人数下限为 3
  assert.ok(MECHS['m11-gripe-fest'].min >= 3, 'M11 至少三人');
});

test('PARAMS 平衡参数齐全且合法', () => {
  assert.ok(PARAMS.BEAT > 0 && PARAMS.CHANCE > 0 && PARAMS.CHANCE <= 1);
  assert.ok(PARAMS.COOLDOWN > 0 && PARAMS.MAX_GROUPS >= 1);
  assert.ok(PARAMS.SIGHT_R > 0 && PARAMS.MOOD_T > 0);
});

test('台词池达指标（批评 8+/回应 6+/自言 4+，t_133 定稿）', () => {
  const count = (pool) => Object.values(pool).reduce((n, a) => n + a.length, 0);
  assert.ok(count(LINES.criticize) >= 8, `批评语 ${count(LINES.criticize)} < 8`);
  assert.ok(count(LINES.reply) >= 6, `回应 ${count(LINES.reply)} < 6`);
  assert.ok(count(LINES.solo) >= 4, `自言 ${count(LINES.solo)} < 4`);
  // 画像标签档存在
  for (const tag of ['orchestrator', 'hr', 'dev', 'plain']) {
    assert.ok(LINES.criticize[tag] || LINES.criticize.plain, `批评语缺 ${tag} 池`);
  }
});
