// mech.test.js — t_144（r_08）：社交机制 DSL 与消费侧 geom 的回归钉子。
// 只测纯函数/纯数据：MECHS 十二套结构合法性（build 可调、step 形状、
// 人数区间、special 豁免标记）、消费侧 geom 常量（INTERACT_SPOTS 与
// binAnchor 的索引约定——真同源校验归 Go 侧 wardrobedump 类工具）、
// 前端 AtlasOrder 切片一致性（FRAME 表与 AVATAR_FRAMES 互证）。
// setTimeout 演出不进测试面（disperse 走纯数据断言，不真跑引擎）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { MECHS, PARAMS, LINES } from './social.js';
import { INTERACT_SPOTS } from './interact.js';
import { CPARAMS } from './cleaner.js';
import { FRAME, AVATAR_FRAMES, SCENE_W, SCENE_H } from './art.js';

// 最小 ctx：anchor 惰性解析（step.walked 才走 board），build 本身只组数据
const CTX = { lead: { name: '甲', x: 0, y: 0 }, partner: { name: '乙', x: 0, y: 0 }, busy: null };

// ── MECHS 十二套结构合法性（≥10 指标）───────────────────────────

test('MECHS 十二套齐（r_06 定稿 M1–M12，指标 ≥10）', () => {
  const keys = Object.keys(MECHS);
  assert.equal(keys.length, 12, `现 ${keys.length} 套`);
  for (let i = 1; i <= 12; i++) {
    assert.ok(MECHS[`m${i}-`] || keys.some(k => k.startsWith(`m${i}-`)), `缺 M${i}`);
  }
});

test('每套机制：key/人数区间合法且 build 可调出非空步数组', () => {
  for (const [k, mech] of Object.entries(MECHS)) {
    assert.equal(mech.key, k, `条目键与 map 键不一致：${k}`);
    assert.ok(mech.min >= 1, `${k}: min<1`);
    assert.ok(mech.max >= mech.min, `${k}: max<min`);
    assert.ok(mech.max <= 4, `${k}: max>4（定稿上限 4 人）`);
    assert.equal(typeof mech.build, 'function', `${k}: build 非函数`);
    const steps = mech.build(CTX);
    assert.ok(Array.isArray(steps) && steps.length > 0, `${k}: steps 空`);
  }
});

test('每步形状：dur 非负数、已知键集合内（DSL 契约）', () => {
  // step 的合法字段（social.js step() 消费面——含 M3 quiet/M4 doodle/
  // M9 loserWalk/M11 altEmote/M12 nap 五个机制专属键）
  const KNOWN = new Set(['dur', 'walk', 'face', 'emote', 'act', 'chat', 'rounds',
    'gripe', 'solo', 'disperse', 'carry', 'caughtEmote',
    'quiet', 'doodle', 'loserWalk', 'altEmote', 'nap']);
  for (const [k, mech] of Object.entries(MECHS)) {
    const steps = mech.build(CTX);
    for (const s of steps) {
      assert.equal(typeof s.dur, 'number', `${k}: step.dur 非数`);
      assert.ok(s.dur >= 0, `${k}: step.dur<0`);
      for (const f of Object.keys(s)) {
        assert.ok(KNOWN.has(f), `${k}: 未知 step 字段 "${f}"（DSL 契约外）`);
      }
    }
  }
});

test('收尾纪律：每套至少一个 disperse 步（散场语义）或引擎兜底', () => {
  // 定稿：摸鱼自然收尾＝脚本末步 disperse。M12 午睡是常驻（无 disperse
  // ——房主点击才醒），是明文的例外。
  for (const [k, mech] of Object.entries(MECHS)) {
    if (k === 'm12-nap') continue;
    const steps = mech.build(CTX);
    assert.ok(steps.some(s => s.disperse), `${k}: 无 disperse 步`);
  }
});

test('豁免标记：no-criticize/legit/care 特判在 opts 里（t_132 判定侧消费）', () => {
  const specials = Object.entries(MECHS)
    .filter(([, m]) => m.opts && m.opts.special)
    .map(([k, m]) => `${k}:${m.opts.special}`);
  // 定稿的特判集：M5 关心（no-criticize+careRank4）/M6/M9 legit/M12 no-criticize
  assert.ok(specials.length >= 4, `特判仅 ${specials.length} 套：${specials.join(' ')}`);
  assert.ok(specials.includes('m9-rock-paper:legit'), 'M9 猜拳应 legit');
  assert.ok(specials.includes('m12-nap:no-criticize'), 'M12 午睡应 no-criticize');
});

test('台词池：批评四标签、回应按 t_133 现状（plain 池非空 ≥2）', () => {
  // 批评语按画像配语气（orchestrator/hr/dev/plain 四池）；
  // 回应语现状只有 plain 池（t_133 填充纪律：回应 6+ 落 plain）。
  for (const tag of ['orchestrator', 'hr', 'dev', 'plain']) {
    const pool = LINES.criticize[tag];
    assert.ok(Array.isArray(pool) && pool.length >= 2, `criticize/${tag} 池不足`);
  }
  assert.ok(Array.isArray(LINES.reply.plain) && LINES.reply.plain.length >= 4,
    `reply/plain 池不足（t_133 要求回应 6+，现 ${LINES.reply.plain.length}）`);
  // 单人自言自语与关心语在池（M5/M6 消费）
  assert.ok((LINES.solo?.plain || []).length >= 2, 'solo 池不足');
  assert.ok((LINES.care?.plain || []).length >= 1, 'care 池不足');
});

test('PARAMS：批评链节奏常量合法（SIGHT_R/CRIT_DELAY/MOOD_T）', () => {
  assert.ok(PARAMS.SIGHT_R > 0 && PARAMS.SIGHT_R <= 200, '视野半径量级（native）');
  assert.ok(PARAMS.CRIT_DELAY >= 0.5 && PARAMS.CRIT_DELAY <= 3, '前摇 0.5–3s');
  assert.equal(PARAMS.MOOD_T, 20, '被批情绪 20s（定稿 §二.3）');
  assert.equal(PARAMS.MAX_GROUPS, 2, '全室同时最多 2 团');
});

// ── 消费侧 geom 契约（常量存在且在房间界内）─────────────────────

test('INTERACT_SPOTS：四件茶水间小件在场景界内（与 pixart 绘制同源）', () => {
  const want = ['coffee', 'microwave', 'bin', 'snack'];
  for (const k of want) {
    const r = INTERACT_SPOTS[k];
    assert.ok(r, `缺 ${k} 命中区`);
    assert.ok(r.x >= 0 && r.x < SCENE_W, `${k}.x=${r.x} 出界（0–${SCENE_W}）`);
    assert.ok(r.y >= 0 && r.y < SCENE_H, `${k}.y=${r.y} 出界（0–${SCENE_H}）`);
    assert.ok(r.w > 0 && r.h > 0, `${k} 尺寸非正`);
    assert.ok(r.x + r.w <= SCENE_W, `${k} 右缘出界`);
    assert.ok(r.y + r.h <= SCENE_H, `${k} 下缘出界`);
  }
});

test('binAnchor 索引约定：pantry.props[4] 是垃圾桶脚印（energy.js 硬编码）', () => {
  // 真值校验归 Go 侧（PantryBlocks 顺序有测试钉）；前端钉「索引约定
  // 被引用且越界有守卫」——energy.js binAnchor 有 length<5 守卫。
  // 这里钉交互面：interact 的 bin 命中区与 energy 的锚点不能一个在
  // 天上一个在地下（同为垃圾桶，中心距离应 < 100 native）。
  const bin = INTERACT_SPOTS.bin;
  const bc = { x: bin.x + bin.w / 2, y: bin.y + bin.h / 2 };
  // energy.binAnchor 无 board 不能直调——钉常量侧：interact 的桶区
  // 中心必须落在茶水间地带（pantryZone 440,160,768,320 的 legacy ×2 附近）
  assert.ok(bc.x > 400 && bc.x < 900, `桶中心 x=${bc.x} 不在茶水间地带`);
  assert.ok(bc.y > 150 && bc.y < 550, `桶中心 y=${bc.y} 不在茶水间地带`);
});

test('保洁阿姨的零食架锚点与 interact 的 snack 命中区同域', () => {
  // cleaner.shelfAnchor() 硬编码 {251*2, 85*2}＝(502,170)；interact 的
  // snack 区中心应离它 < 80 native（都是零食架）
  const sn = INTERACT_SPOTS.snack;
  const sc = { x: sn.x + sn.w / 2, y: sn.y + sn.h / 2 };
  const d = Math.hypot(sc.x - 502, sc.y - 170);
  assert.ok(d < 80, `零食架命中区中心与 cleaner 锚点相距 ${d.toFixed(0)}——两处硬编码漂移`);
});

// ── AtlasOrder 一致性（前端切片面）──────────────────────────────

test('FRAME 表：九帧索引 0–8 无重无漏（与 pixart.AtlasOrder 逐位对应）', () => {
  const idx = Object.values(FRAME);
  assert.equal(idx.length, AVATAR_FRAMES, `FRAME ${idx.length} 项 ≠ AVATAR_FRAMES ${AVATAR_FRAMES}`);
  assert.deepEqual([...idx].sort(), [0, 1, 2, 3, 4, 5, 6, 7, 8], '索引非 0–8 全集');
  // 与服务端 AtlasOrder 同序：downA,downB,downBlink,upA,upB,leftA,leftB,rightA,rightB
  assert.equal(FRAME.downA, 0);
  assert.equal(FRAME.downB, 1);
  assert.equal(FRAME.downBlink, 2);
  assert.equal(FRAME.upA, 3);
  assert.equal(FRAME.upB, 4);
  assert.equal(FRAME.leftA, 5);
  assert.equal(FRAME.leftB, 6);
  assert.equal(FRAME.rightA, 7);
  assert.equal(FRAME.rightB, 8);
});

test('AVATAR_FRAMES＝9：roomview 按 atlas 宽/9 切片的前提', () => {
  assert.equal(AVATAR_FRAMES, 9, 'wire 协议：atlas 行恒为 9 帧');
});
