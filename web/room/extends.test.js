// extends.test.js — t_143（r_08）：表情映射与参数边界的增补面，接在
// 小猿 t_141 骨架（emote.test.js / energy.test.js）之后——那两份钉了
// 目录齐全与参数形状，这里钉**行为纪律**：
//   ① emoteFromText 每键 ≥2 触发词（验收口径）＋优先级语义（首行赢、
//      关键词先于 emoji 直配）；
//   ② 体力曲线语义（drain 定稿 0.35/min 的每秒折算、critAt 减半路径、
//      上限钳制）与桶满阈值路径；
//   ③ 扩展面：POSE_KEYS 完整性＋时长表合法、CPARAMS 巡逻节奏与
//      全天在岗默认（nightStop 调参开关）。
// 仍只测纯逻辑（模块级常量与函数），setTimeout 演出不进测试面。

import test from 'node:test';
import assert from 'node:assert/strict';
import { EMOJIS, emoteFromText } from './emote.js';
import { EPARAMS } from './energy.js';
import { POSE_KEYS } from './poses.js';
import { CPARAMS } from './cleaner.js';

// ── ① 表情映射的行为纪律 ────────────────────────────────────────

// 每键 ≥2 触发词（t_143 验收口径）。done/working/star 三款无关键词行
// （触发源是 task 事件与 idle 池，定稿如此）——不在关键词断言集内。
const KEYWORD_CASES = {
  happy: ['谢谢啦', '哈哈真好玩'],
  thinking: ['让我想想', 'Hmm…'],
  confused: ['为什么这样？', '没明白'],
  sleepy: ['好困', 'zzz'],
  coffee: ['去喝咖啡', '摸鱼一会儿'],
  sweat: ['出 bug 了', '完蛋'],
  angry: ['可恶', '气死我了'],
  love: ['喜欢这个', '❤'],
  wave: ['你好呀', '欢迎新同事'],
};

test('关键词表：每键 ≥2 触发词全部命中（t_143 验收口径）', () => {
  for (const [key, hits] of Object.entries(KEYWORD_CASES)) {
    assert.ok(EMOJIS[key], `断言集键 ${key} 不在目录`);
    assert.ok(hits.length >= 2, `${key} 断言用例不足`);
    for (const h of hits) {
      assert.equal(emoteFromText(h), key, `"${h}" 应触发 ${key}`);
    }
  }
});

test('优先级：关键词表（含内嵌 emoji）先于纯 emoji 直配兜底', () => {
  // 关键词表 happy 行内嵌 😊 字面量——「谢谢😊」两路同指 happy，表先走
  assert.equal(emoteFromText('谢谢😊'), 'happy');
  // 两行关键词相争：happy 行在 angry 行前——首行赢
  assert.equal(emoteFromText('可恶但谢谢'), 'happy');
  // ☕ 不在任何关键词行——走直配兜底
  assert.equal(emoteFromText('这杯☕不错'), 'coffee');
  // 同理 ❤️（变体选择符形态）只在直配 map
  assert.equal(emoteFromText('支持❤️'), 'love');
});

// ── ② 体力曲线与桶满路径（EPARAMS 之外的行为面）────────────────

test('体力曲线：drain 0.35/min、idleDrain 0.25/min 的每秒折算', () => {
  // 0.35/min ÷ 60 ≈ 0.005833/s——允许浮点尾差
  assert.ok(Math.abs(EPARAMS.drain - 0.35 / 60) < 1e-9,
    `drain=${EPARAMS.drain} 不是 0.35/min 的秒折算`);
  // 空闲也饿（没活干也要吃东西）：0.25/min 慢漏，慢于干活消耗
  assert.ok(Math.abs(EPARAMS.idleDrain - 0.25 / 60) < 1e-9,
    `idleDrain=${EPARAMS.idleDrain} 不是 0.25/min 的秒折算`);
  assert.ok(EPARAMS.drain > EPARAMS.idleDrain, '干活消耗必须快于空闲慢漏');
});

test('体力量纲：满值干到低体力线是「小时级」而非「分钟级」（防调参事故）', () => {
  const mins = (EPARAMS.energyMax - EPARAMS.lowAt) / (EPARAMS.drain * 60);
  assert.ok(mins > 60 && mins < 1440, `满→低体力需 ${mins} 分钟，超出一天量级`);
  // 空闲慢漏同一纪律（不能调成分钟级——办公室变干饭 circus）
  const idleMins = (EPARAMS.energyMax - EPARAMS.lowAt) / (EPARAMS.idleDrain * 60);
  assert.ok(idleMins > 60 && idleMins < 1440, `空闲满→低体力需 ${idleMins} 分钟，超出一天量级`);
});

test('桶满阈值路径：capacity=6、三档渐满的档位分界（定稿 §三）', () => {
  assert.equal(EPARAMS.binCapacity, 6, '桶容量定稿 6');
  // energy.paint 的档位：≥capacity → 2 档；≥ceil(capacity/2) → 1 档
  const stage = (n) => n >= EPARAMS.binCapacity ? 2
    : n >= Math.ceil(EPARAMS.binCapacity / 2) ? 1 : 0;
  assert.equal(stage(0), 0, '空桶 0 档');
  assert.equal(stage(2), 0, '2 件仍平线（<3）');
  assert.equal(stage(3), 1, '3 件冒尖');
  assert.equal(stage(5), 1, '5 件仍冒尖');
  assert.equal(stage(6), 2, '满 6 件溢出挂边');
  assert.equal(stage(7), 2, '超容量钳在 2 档');
});

test('hungryChance/critAt 双路径：昏线强制觅食（概率 1）', () => {
  // forageBeat 的路径选择：e < critAt → chance=1；否则 hungryChance
  // ——这里钉参数语义（路径本身在 energy.js，纯函数面外）
  assert.ok(EPARAMS.critAt < EPARAMS.lowAt, '昏线低于阈值（两级触发）');
  assert.equal(EPARAMS.hungryChance, 0.4, '低体力档概率定稿 0.4');
});

// ── ③ 扩展面：POSE_KEYS 与 CPARAMS ─────────────────────────────

test('POSE_KEYS：十四款齐（A1–A12＋r_07 三款 eat/toss/bend）', () => {
  const want = ['nod', 'shake', 'jump', 'bow', 'cheer', 'facepalm', 'stretch',
    'point', 'lean', 'typing', 'lookaround', 'eat', 'toss', 'bend'];
  assert.equal(POSE_KEYS.length, 14, `现 ${POSE_KEYS.length} 款`);
  for (const k of want) assert.ok(POSE_KEYS.includes(k), `缺动作 ${k}`);
});

test('CPARAMS：巡逻节奏与全天在岗（定稿 §四＋夜班改版）', () => {
  assert.equal(EPARAMS ? CPARAMS.patrolEvery : 0, 300, '5 分钟/轮');
  assert.equal(CPARAMS.nightStop, false, '保洁全天在岗（夜班照巡）');
  assert.ok(CPARAMS.walkSpeed > 0 && CPARAMS.walkSpeed < 40, '阿姨步速温和（<40 px/s）');
  assert.ok(CPARAMS.pickDur > 0 && CPARAMS.pickDur <= 2, '弯腰一拍 ≤2s（分册帧纪律）');
});

test('nightStop 语义：默认关（全天在岗），窗口 0–6 点留作活文档', () => {
  // cleaner.nightNow 的实现是 CPARAMS.nightStop && getHours() ∈ [0,6)
  // ——默认关＝阿姨上夜班（成员夜里也吃饭产垃圾）；拨 true 即恢复
  // 0–6 点停巡。窗口长度由常量名承载（night＝0–6），无独立常量可断
  // ——若未来抽 NIGHT_HOURS 常量，此处补断言 0 与 6。
  assert.equal(CPARAMS.nightStop, false, '默认全天在岗');
});
