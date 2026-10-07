// replay.test.js — r_23 t_198：环形缓冲与影子重建的纯逻辑面。假时钟
// 构造帧序列直喂（collide.test 同模式），覆盖验收 1/2 的引擎口径。
// r_32 增补：九字段帧面、日缓冲折入、变化过滤、走路摆帧。

import test from 'node:test';
import assert from 'node:assert/strict';
import { ReplayBuf, shadowAt, shadowRoomAt, frameOf, dayBufOf, sameSample, shadowFrame } from './replay.js';

// mock 演员（frameOf 读的字段面）。泡形同 bubbles.js makeBubble 的真实
// 产出——文本在 raw、分层在 report/mention/mirror（r_32.1 根因：首版
// mock 造 {text} 假形状，掩盖了 frameOf 读不存在 .text 的断线）。r_32.2
// 补人物面：会籍/宽限/职级/本人/阿姨/纸弹。
const mk = (name, x, y, dir = 0, bubble = '', working = false, extra = {}) => ({
  name, x, y, dir, working,
  bubble: bubble ? {
    raw: bubble,
    report: !!extra.report, mention: !!extra.mention, mirror: !!extra.mirror,
  } : null,
  holding: extra.holding || null,
  foodKey: extra.foodKey || '',
  color: extra.color || '',
  hair: extra.hair || '',
  label: extra.label || '',
  deskIdx: typeof extra.deskIdx === 'number' ? extra.deskIdx : -1,
  doneT: extra.doneT || 0,
  meetIdx: typeof extra.meetIdx === 'number' ? extra.meetIdx : -1,
  grace: !!extra.grace,
  rank: typeof extra.rank === 'number' ? extra.rank : null,
  isLocal: !!extra.isLocal,
  npcCleaner: !!extra.npcCleaner,
  sheetPopT: extra.sheetPopT || 0,
});

test('采样：帧进缓冲按 t 升序', () => {
  const buf = new ReplayBuf();
  buf.sample(0.5, [mk('甲', 10, 20)]);
  buf.sample(1.0, [mk('甲', 20, 20)]);
  buf.sample(1.5, [mk('甲', 30, 20)]);
  assert.equal(buf.frames.length, 3);
  assert.equal(buf.latestT, 1.5);
  assert.equal(buf.earliestT, 0.5);
});

// ② 环形挤出：保 10 分钟（600s），旧帧出队。
test('容量裁剪：环形 600s 窗挤出最旧', () => {
  const buf = new ReplayBuf();
  for (let t = 0; t <= 700; t += 0.5) {
    buf.sample(t, [mk('甲', t, 0)]);
  }
  // 首帧之后所有 ≤ (700-600)=100 的帧被挤出
  assert.ok(buf.earliestT <= 100.5, `earliest ${buf.earliestT}`);
  assert.ok(buf.earliestT > 99, '首帧保底不空');
  assert.equal(buf.latestT, 700);
});

// ③ 插值：T 在两帧之间→位置线性插值 ±1px。
test('影子重建：插值位置 ±1px', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0)]);
  buf.sample(10.5, [mk('甲', 13, 0)]); // 13px 步长（0.5s×26px/s）
  const sh = shadowAt(buf, 10.25);
  assert.equal(sh.length, 1);
  assert.ok(Math.abs(sh[0].x - 6.5) < 1, `x=${sh[0].x}`);
});

// ④ 朝向/泡取前帧离散值（§一）。
test('影子重建：朝向与泡取前帧', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0, 2, '你好', true)]);
  buf.sample(10.5, [mk('甲', 10, 0, 1, '变了', false)]);
  const sh = shadowAt(buf, 10.3);
  assert.equal(sh[0].dir, 2, '朝向取前帧');
  assert.equal(sh[0].bubble, '你好', '泡取前帧');
  assert.equal(sh[0].working, true, '工作态取前帧');
});

// ⑤ 后帧掉线者不画（保守消影）。
test('影子重建：后帧离场者消影', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0), mk('乙', 5, 5)]);
  buf.sample(10.5, [mk('甲', 10, 0)]); // 乙离场
  const sh = shadowAt(buf, 10.25);
  assert.equal(sh.length, 1);
  assert.equal(sh[0].name, '甲');
});

// ⑥ T 超界：钳到边界帧（earliest/latest）。
test('影子重建：T 超界钳边', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 1, 1)]);
  buf.sample(11, [mk('甲', 99, 99)]);
  assert.equal(shadowAt(buf, 5)[0].x, 1, 'T<earliest 钳首帧');
  assert.equal(shadowAt(buf, 99)[0].x, 99, 'T>latest 钳末帧');
});

// ⑦ 同栅格重采样：覆盖不双插。
test('采样：同栅格覆盖', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0)]);
  buf.sample(10, [mk('甲', 5, 0)]); // resize 抖动同拍
  assert.equal(buf.frames.length, 1);
  assert.equal(buf.frames[0].list[0].x, 5);
});

// ⑧ frameOf 泡文本帽（r_32.1 从 24 放宽到 120——live 泡四行折行预览的
// 全量；超出绘制侧 makeBubble 照常折行省略）。
test('frameOf：泡文本读 raw＋截断 120 字', () => {
  const long = mk('甲', 0, 0, 0, '一'.repeat(200));
  const f = frameOf([long]);
  assert.equal(f[0].b.length, 120);
  // 回归锚（r_32.1 根因）：泡文本必须出在 raw 上——.text 从不存在，
  // 读它的版本气泡一帧都没录上。
  assert.equal(frameOf([mk('甲', 0, 0, 0, '你好')])[0].b, '你好');
});

// ── r_32 一日回放：九字段帧面＋日缓冲＋变化过滤＋走路摆帧 ──────────

// ⑨ 九字段：hold/c/hair 入帧（外观自足键——分享页离场成员取图靠它）。
test('frameOf：九字段（hold/c/hair 在账）', () => {
  const f = frameOf([mk('甲', 10, 20, 2, '你好', true, { holding: 'sheet', color: '#a03', hair: '01' })])[0];
  assert.equal(f.hold, 'sheet');
  assert.equal(f.c, '#a03');
  assert.equal(f.hair, '01');
  assert.deepEqual(Object.keys(f).sort(),
    ['b', 'c', 'd', 'hair', 'hold', 'n', 'w', 'x', 'y']);
});

// ⑨-b r_32.1 头顶面五列：泡分层/工位/任务牌/落定 ✓/食品键入帧
//（缺省值不上账——变化过滤的噪声面越小越好）。
test('frameOf：头顶面五列（bt/dk/lab/dn/fk）', () => {
  const f = frameOf([mk('甲', 10, 20, 2, '汇报正文', true, {
    holding: 'food', foodKey: 'coffee', report: true,
    deskIdx: 0, label: 't_9', doneT: 2,
  })])[0];
  assert.equal(f.bt, 1, '汇报分层');
  assert.equal(f.dk, 0, '工位 0 必须在账（0 是真工位，不是缺省）');
  assert.equal(f.lab, 't_9');
  assert.equal(f.dn, true);
  assert.equal(f.fk, 'coffee');
  const plain = frameOf([mk('乙', 0, 0)])[0];
  for (const key of ['bt', 'dk', 'lab', 'dn', 'fk']) {
    assert.ok(!(key in plain), `${key} 缺省不上账`);
  }
});

// ⑩ 日缓冲：服务端行折入（t=墙钟 ms），坏行/乱序行丢弃。
test('dayBufOf：行折入＋坏行乱序护门', () => {
  const rows = [
    null,
    { t: 1000, a: [{ n: '甲', x: 1, y: 1, d: 0 }] },
    { t: 900, a: [{ n: '甲', x: 0, y: 0, d: 0 }] },   // 乱序丢
    { bad: true },                                     // 坏行丢
    { t: 1500, a: [{ n: '甲', x: 2, y: 2, d: 0 }] },
  ];
  const buf = dayBufOf(rows);
  assert.deepEqual(buf.frames.map((f) => f.t), [1000, 1500]);
  assert.equal(buf.span, 500);
});

// ⑪ 影子透传 hold/c/hair，walking 由括号位移推断。
test('shadowAt：外观键透传＋walking 推断', () => {
  const buf = new ReplayBuf();
  const a1 = mk('甲', 0, 0, 2, '', true, { holding: 'sheet', color: '#a03', hair: '01' });
  const a2 = mk('甲', 13, 0, 2, '', true, { holding: 'sheet', color: '#a03', hair: '01' });
  buf.sample(10, [a1]);
  buf.sample(10.5, [a2]);
  const sh = shadowAt(buf, 10.25);
  assert.equal(sh[0].hold, 'sheet');
  assert.equal(sh[0].c, '#a03');
  assert.equal(sh[0].hair, '01');
  assert.equal(sh[0].walking, true, '0.5s 走 13px 是走路');

  const still = new ReplayBuf();
  still.sample(10, [{ n: '甲', x: 0, y: 0, d: 0, b: '', w: false }]);
  still.sample(10.5, [{ n: '甲', x: 0.25, y: 0, d: 0, b: '', w: false }]);
  assert.equal(shadowAt(still, 10.25)[0].walking, false, '亚像素抖动不是走路');
});

// ⑪-b r_32.1 泡的完整生命：起源扫描定出现时刻，寿命＝起源＋6s 与
// 「同文游程后首帧」双钳——稀疏日缓冲的漫长空档不再把前帧的泡定格
// 到天荒地老（说一句泡 6s 消，跟实时同一节拍）。
test('shadowAt：泡生命（起源→6s 寿终；空档不定格）', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0, 0, '你好')]);
  buf.sample(10.5, [mk('甲', 1, 0, 0, '你好')]); // 泡持续中（位移是另一维度）
  buf.sample(400, [mk('甲', 1, 0)]);              // 空档后泡没了
  let sh = shadowAt(buf, 10.3)[0];
  assert.equal(sh.bubble, '你好', '泡文本在（前帧离散值）');
  assert.ok(Math.abs(sh.bubbleTtl - 5.7) < 1e-9, `ttl=${sh.bubbleTtl}（起源 10s＋6s 寿命）`);
  assert.ok(sh.speakT > 0 && sh.speakT <= 0.5, '说话弹跳在 SPEAK_BEAT 窗内');
  sh = shadowAt(buf, 100)[0];
  assert.equal(sh.bubble, '', '寿终（16s）之后泡不画——旧版定格到 400s');
  assert.equal(sh.bubbleTtl, 0);
  sh = shadowAt(buf, 15.9)[0];
  assert.ok(Math.abs(sh.bubbleTtl - 0.1) < 1e-9, '临终 0.4s 淡出窗（绘制侧读 ttl）');
});

// ⑪-c 换文即死：新话顶掉旧话时，死亡钳在换文帧（不是旧起源＋6s）。
test('shadowAt：泡被新话顶掉（死亡钳在换文帧）', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0, 0, '第一句')]);
  buf.sample(11, [mk('甲', 0, 0, 0, '第二句')]);
  const sh = shadowAt(buf, 10.9)[0];
  assert.equal(sh.bubble, '第一句');
  assert.ok(Math.abs(sh.bubbleTtl - 0.1) < 1e-9, `ttl=${sh.bubbleTtl}（11s 换文帧即死）`);
});

// ⑪-d 分层/工位/任务牌/手持透传（绘制面同字段名——坐席泡锚定、🔨
// 任务牌、手持件全靠它们）；镜像泡不弹跳（实时 say() 同口径）。
test('shadowAt：头顶面透传＋镜像不弹跳', () => {
  const buf = new ReplayBuf();
  const ex = { report: true, deskIdx: 3, label: 't_9', holding: 'food', foodKey: 'coffee' };
  buf.sample(10, [mk('甲', 5, 5, 0, '汇报', true, ex)]);
  buf.sample(10.5, [mk('甲', 5, 5, 0, '汇报', true, ex)]);
  const sh = shadowAt(buf, 10.2)[0];
  assert.equal(sh.bt, 1, '汇报分层透传');
  assert.equal(sh.deskIdx, 3, '工位透传（坐席泡锚定/任务牌门槛读它）');
  assert.equal(sh.label, 't_9');
  assert.equal(sh.holding, 'food');
  assert.equal(sh.foodKey, 'coffee');
  const mir = new ReplayBuf();
  mir.sample(10, [mk('乙', 0, 0, 0, '回声', false, { mirror: true })]);
  mir.sample(10.5, [mk('乙', 0, 0, 0, '回声', false, { mirror: true })]);
  assert.equal(shadowAt(mir, 10.2)[0].speakT, 0, '镜像泡不弹跳');
});

// ⑪-e 落定 ✓ 谢幕钟：起源帧起 2s 随时间淡出（t_104 同款）。
test('shadowAt：落定 ✓ 的谢幕钟', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0, 0, '', true, { doneT: 2 })]);
  buf.sample(10.5, [mk('甲', 0, 0, 0, '', true, { doneT: 1.5 })]);
  buf.sample(20, [mk('甲', 0, 0, 0, '', true)]);
  assert.ok(Math.abs(shadowAt(buf, 10.3)[0].doneT - 1.7) < 1e-9, '剩余随时间走');
  assert.equal(shadowAt(buf, 15)[0].doneT, 0, '2s 后谢幕');
});

// ⑪-f 表情泡：键从泡文本确定性推出（实时 say→onSay 同一规则），2s 窗。
test('shadowAt：表情泡键随泡文本（2s 窗）', () => {
  const buf = new ReplayBuf();
  buf.sample(10, [mk('甲', 0, 0, 0, '😊')]);
  buf.sample(30, [mk('甲', 0, 0)]);
  let sh = shadowAt(buf, 10.5)[0];
  assert.equal(sh.emote, 'happy');
  assert.ok(sh.emoteT > 0 && sh.emoteT <= 2);
  assert.equal(shadowAt(buf, 13)[0].emote, null, '2s 窗外不画');
});

// ⑪-g day 档 ms 帧面：生命按秒换算（u=1000），ring/day 同一节拍。
test('shadowAt：day 档 ms 帧面的生命换算', () => {
  const buf = dayBufOf([
    { t: 10_000, a: frameOf([mk('甲', 0, 0, 0, '你好')]) },
    { t: 70_000, a: frameOf([mk('甲', 0, 0)]) },
  ]);
  const sh = shadowAt(buf, 13_000)[0];
  assert.equal(sh.bubble, '你好');
  assert.ok(Math.abs(sh.bubbleTtl - 3) < 1e-9, `ttl=${sh.bubbleTtl}（3s 后寿终）`);
  assert.equal(shadowAt(buf, 20_000)[0].bubble, '', 'ms 档寿终同样消泡');
});

// ── r_32.2 全量视态：人物列＋房间级 r ─────────────────────────────

// ⑫-a 人物面入帧：mi/gr/rk/loc/npc/po/em/sp（live 查询面喂 po/em）。
test('frameOf：人物面八列（mi/gr/rk/loc/npc/po/em/sp）', () => {
  const f = frameOf(
    [mk('甲', 0, 0, 0, '', false, { meetIdx: 2, grace: true, rank: 3, isLocal: true, sheetPopT: 1.5 })],
    { poseOf: () => ({ key: 'cheer', t: 0.4 }), emoteOf: () => 'happy' })[0];
  assert.equal(f.mi, 2);
  assert.equal(f.gr, true);
  assert.equal(f.rk, 3);
  assert.equal(f.loc, true);
  assert.equal(f.po, 'cheer');
  assert.equal(f.em, 'happy');
  assert.equal(f.sp, true);
  const plain = frameOf([mk('乙', 0, 0)], { poseOf: () => null, emoteOf: () => null })[0];
  for (const key of ['mi', 'gr', 'rk', 'loc', 'npc', 'po', 'em', 'sp']) {
    assert.ok(!(key in plain), `${key} 缺省不上账`);
  }
});

// ⑫-b 影子透传人物面＋动作生命（POSE_DUR 钳）＋纸弹谢幕＋表情泡记录列
// 权威（覆盖泡文本推导——react 顶掉 say 的实时语义）。
test('shadowAt：人物面透传＋动作/纸弹/表情泡生命', () => {
  const buf = new ReplayBuf();
  const a1 = { n: '甲', x: 0, y: 0, d: 0, b: '😊', em: 'love', po: 'jump', sp: true, mi: 1, gr: true, rk: 2, loc: true };
  const a2 = { ...a1 };
  buf.frames.push({ t: 10, list: [a1] });
  buf.frames.push({ t: 10.5, list: [a2] });
  buf.frames.push({ t: 30, list: [{ n: '甲', x: 0, y: 0, d: 0, b: '' }] });
  let sh = shadowAt(buf, 10.2)[0];
  assert.equal(sh.emote, 'love', '记录列 em 权威（泡文本会推 happy，实时语义 react 顶掉 say）');
  assert.equal(sh.pose, 'jump');
  assert.ok(sh.poseT > 0 && sh.poseT < 1.0, `poseT=${sh.poseT}（已进行秒）`);
  assert.ok(sh.sheetPopT > 0 && sh.sheetPopT <= 1.5, '纸弹谢幕钟在');
  assert.equal(sh.meetIdx, 1);
  assert.equal(sh.grace, true);
  assert.equal(sh.rank, 2);
  assert.equal(sh.isLocal, true);
  sh = shadowAt(buf, 13)[0];
  assert.equal(sh.emote, null, '表情泡 2s 窗过');
  assert.equal(sh.pose, '', 'jump 1s 寿终');
  assert.equal(sh.sheetPopT, 0, '纸弹 1.5s 寿终');
});

// ⑫-c 房间级视态还原：离散面取前帧，瞬时态沿时长表前进——空档里各
// 自走完生命，不定格、不穿越。
test('shadowRoomAt：房间态还原＋瞬时态前进', () => {
  const buf = new ReplayBuf();
  buf.frames.push({
    t: 10,
    list: [{ n: '甲', x: 0, y: 0, d: 0, b: '' }],
    room: {
      lit: true, note: '今日公告', pour: 1.0,
      pw: { 2: 6 },
      prn: { job: { ph: 'light', k: 'task', t: 0.25 }, un: [['done', 3]] },
      en: { bin: 2, snacks: 0, lt: [[10, 20, 0, 'coffee']] },
      tro: 3,
      ia: [['coffee', 0.25, 560, 154]],
    },
  });
  buf.frames.push({ t: 400, list: [{ n: '甲', x: 0, y: 0, d: 0, b: '' }], room: { lit: false } });
  let VS = shadowRoomAt(buf, 10.5);
  assert.equal(VS.lit, true, '大屏点亮（前帧离散值）');
  assert.equal(VS.note, '今日公告');
  assert.ok(Math.abs(VS.pourT - 0.5) < 1e-9, `pourT=${VS.pourT}（1.0 起前进 0.5s）`);
  assert.ok(Math.abs(VS.plantWater.get(2) - (VS.clock - 5.5)) < 1e-9, '浇花剩余 5.5s（born 反推）');
  assert.equal(VS.prn.job.ph, 'paper', '打印相位前进 light→paper（0.25+0.5 ≥ LIGHT 0.5）');
  assert.ok(Math.abs(VS.prn.job.t - 0.25) < 1e-9, `相位内已进行 ${VS.prn.job.t}s`);
  assert.ok(Math.abs(VS.prn.un[0][1] - 2.5) < 1e-9, '滞留纸剩余衰减');
  assert.ok(Math.abs(VS.en.lt[0].age - 0.5) < 1e-9, '垃圾龄前进');
  assert.equal(VS.en.snacks, 0, '售罄面透传');
  assert.equal(VS.tro, 3, '奖杯数透传');
  assert.ok(Math.abs(VS.ia.get('coffee').t - 0.75) < 1e-9, '交互反馈已进行秒前进');
  // 空档深处：瞬时态各自寿终；离散面（lit）按前帧语义撑到下一变化帧
  // ——真实录制里关屏本身就会落一帧变化帧，间隙＝0.5s 采样精度
  VS = shadowRoomAt(buf, 50);
  assert.equal(VS.lit, true, '离散面取前帧（关屏帧未到，屏还亮——采样诚实）');
  assert.equal(VS.pourT, 0, '倒水早已走完');
  assert.equal(VS.prn.job, null, '打印活早已完（take 之后机器空）');
  assert.equal(VS.prn.un.length, 0, '滞留纸早已收走');
  assert.equal(VS.ia.size, 0, '交互反馈早已寿终');
  assert.equal(VS.en.lt.length, 1, '垃圾 90s 寿命内还在（龄 40）');
  VS = shadowRoomAt(buf, 100);
  assert.equal(VS.en.lt.length, 0, '垃圾 90s 寿终');
  assert.equal(VS.lit, true, '关屏帧（t=400）未到之前屏仍亮');
  VS = shadowRoomAt(buf, 450);
  assert.equal(VS.lit, false, '变化帧到了，离散面切换');
});

// ⑫-d sameRoom／sameSample 的快照形状：房间级 r 一并对比（{a, r} 或
// 裸数组——两种形状都收）。
test('sameSample：快照形状与房间级对比', () => {
  const base = { n: '甲', x: 10, y: 20, d: 2, b: '', w: true, hold: '', c: '#1', hair: '1' };
  const snapA = { a: [{ ...base }], r: { lit: true, note: '一' } };
  assert.equal(sameSample(snapA, { a: [{ ...base }], r: { lit: true, note: '一' } }), true, '房态全同');
  assert.equal(sameSample(snapA, { a: [{ ...base }], r: { lit: true, note: '二' } }), false, '公告变了');
  assert.equal(sameSample(snapA, { a: [{ ...base }], r: { lit: false, note: '一' } }), false, '大屏变了');
  assert.equal(sameSample(snapA, { a: [{ ...base }], r: { pour: 0.5 } }), false, '房态字段集变了');
  assert.equal(sameSample([{ ...base }], [{ ...base }]), true, '裸数组老用法不破（房态同缺省）');
});

// ⑫ 变化过滤：录制腿的对照面（完全同帧 true，任何一字段/半像素动 false）。
test('sameSample：变化过滤口径', () => {
  const a = [{ n: '甲', x: 10, y: 20, d: 2, b: '', w: true, hold: '', c: '#1', hair: '1' }];
  assert.equal(sameSample(a, [{ n: '甲', x: 10.4, y: 20, d: 2, b: '', w: true, hold: '', c: '#1', hair: '1' }]), true);
  assert.equal(sameSample(a, [{ n: '甲', x: 10.6, y: 20, d: 2, b: '', w: true, hold: '', c: '#1', hair: '1' }]), false, '≥0.5px 动了');
  assert.equal(sameSample(a, [{ n: '甲', x: 10, y: 20, d: 3, b: '', w: true, hold: '', c: '#1', hair: '1' }]), false, '朝向变了');
  assert.equal(sameSample(a, [{ n: '甲', x: 10, y: 20, d: 2, b: '话', w: true, hold: '', c: '#1', hair: '1' }]), false, '泡变了');
  const two = a.concat([{ n: '乙', x: 0, y: 0, d: 0, b: '', w: false, hold: '', c: '', hair: '' }]);
  assert.equal(sameSample(a, two), false, '人数变了');
  // r_32.1 五列同在离散面：分层/工位/任务牌/✓/食品任何一变都要落盘
  const base = { n: '甲', x: 10, y: 20, d: 2, b: '', w: true, hold: '', c: '#1', hair: '1' };
  assert.equal(sameSample([base], [{ ...base, bt: 1 }]), false, '泡分层变了');
  assert.equal(sameSample([base], [{ ...base, dk: 2 }]), false, '工位变了');
  assert.equal(sameSample([base], [{ ...base, lab: 't_9' }]), false, '任务牌变了');
  assert.equal(sameSample([base], [{ ...base, dn: true }]), false, '落定 ✓ 亮了');
  assert.equal(sameSample([base], [{ ...base, fk: 'coffee' }]), false, '食品换了');
  assert.equal(sameSample([base], [{ ...base }]), true, '五列同缺省仍是无变化');
});

// ⑬ 走路摆帧：静站姿定格，走路按 6fps 交替 A/B（帧号与 art.js FRAME 对齐）。
test('shadowFrame：站姿定格＋A/B 交替', () => {
  assert.equal(shadowFrame(0, false, 999), 0, 'down 站 downA');
  assert.equal(shadowFrame(1, false, 999), 3, 'up 站 upA');
  assert.equal(shadowFrame(2, false, 999), 7, 'right 站 rightA');
  assert.equal(shadowFrame(3, false, 999), 5, 'left 站 leftA');
  assert.equal(shadowFrame(0, true, 0), 0, '偶拍 downA');
  assert.equal(shadowFrame(0, true, 1 / 6), 1, '奇拍 downB');
  assert.equal(shadowFrame(1, true, 1 / 6), 4, 'up 拍 upB');
  assert.equal(shadowFrame(2, true, 1 / 6), 8, 'right 拍 rightB');
  assert.equal(shadowFrame(3, true, 1 / 6), 6, 'left 拍 leftB');
});

// t_199 验收补钉：采样契约常量（破坏性抽验「改 SAMPLE_EVERY 必红」的锚——
// 原八项用显式 t 驱动不经节拍，抽验无从红；导出常量＋契约断言补上这层）
import { SAMPLE_EVERY, RETAIN } from './replay.js';
test('采样契约：0.5s 定时帧＋600s 环形窗（§一定稿口径）', () => {
  assert.equal(SAMPLE_EVERY, 0.5, '采样间隔＝定稿 0.5s');
  assert.equal(RETAIN, 600, '环形窗＝10 分钟');
  assert.ok(RETAIN / SAMPLE_EVERY >= 1200, '窗内至少 1200 帧');
});

// ── 回放接线契约（静态断言，sfx.test 同款：roomview/plates 的 DOM 链
// 不 import，抽源文本验影子进 paint 链的鸭子面。回放上线首日即坏的回
// 归——sprite 分支裸调 a.frameIndex() 每帧 TypeError、Esc 退出是死码、
// 泡源不换影子——全靠这层锚住）────────────────────────────────────
import { readFileSync } from 'node:fs';
const roomSrc = readFileSync(new URL('./roomview.js', import.meta.url), 'utf8');
const plateSrc = readFileSync(new URL('./plates.js', import.meta.url), 'utf8');

test('接线契约：sprite 分支容影子（frameIndex 不裸调＋借实时 atlas＋摆帧表）', () => {
  assert.match(roomSrc, /a\.frameIndex\s*\?\s*a\.frameIndex\(\)/,
    'frameIndex 必须可缺——影子是普通对象，裸调即每帧 TypeError（回放画面空房根因）');
  assert.match(roomSrc, /this\.actors\.get\(a\.name\)\?\.atlas/,
    '影子图必须借实时同名 atlas（分层签名同人同图）');
  assert.match(roomSrc, /shadowFrame\(a\.dir, a\.walking, this\.replaySec\(\)\)/,
    'r_32 走路摆帧在（相邻帧位移推断 walking，静站定格）');
  assert.match(roomSrc, /a\.c \? this\.art\.atlas\(a\.c, a\.hair/,
    '外观键兜底在（离场成员/分享页无实时 atlas，凭 c/hair 直取）');
});

test('接线契约：speakT 缺省必 0（speakHop(undefined)=NaN 毒化坐标）', () => {
  assert.match(roomSrc, /speakHop\(a\.speakT \|\| 0\)/, 'roomview sprite 分支 hop 守卫');
  assert.match(plateSrc, /speakHop\(a\.speakT \|\| 0\)/, 'drawPlate 名牌 y 守卫');
});

test('接线契约：泡的布局源随回放换影子（实时泡不叠历史画面）', () => {
  assert.match(roomSrc, /drawBubbles\(ctx, this\.replayT >= 0/,
    'drawBubbles 源条件在');
  assert.match(roomSrc, /makeBubble\(ctx, \{\s*\n\s*text: a\.bubble, from: a\.name,/,
    '影子泡文本经 makeBubble 现测成泡记录（layoutBubbles 读 w/h/ttl）');
  assert.match(roomSrc, /report: a\.bt === 1, mention: a\.bt === 2, mirror: a\.bt === 3,/,
    'r_32.1 泡分层按帧里的 bt 复原（汇报蓝/提及金/镜像羊皮纸）');
  assert.match(roomSrc, /seated: a\.working && a\.deskIdx >= 0,/,
    'r_32.1 坐席泡锚定（窄两行、锚桌沿）按帧里的 dk 复原');
  assert.match(roomSrc, /b\.ttl = a\.bubbleTtl;/,
    'r_32.1 ttl 直读影子的剩余生命（弹入/停留/淡出随时间轴，不再定格中段）');
  const replaySrc = readFileSync(new URL('./replay.js', import.meta.url), 'utf8');
  assert.match(replaySrc, /bub && bub\.raw/,
    'frameOf 必须读 makeBubble 的 raw——.text 从不存在（r_32.1 根因：读它的版本气泡一帧都没录上）');
});

test('接线契约：←/→/Esc 在移动键分栏之前到 replayKey（Esc 退出不是死码）', () => {
  const replayGate = roomSrc.indexOf("this.replayT >= 0 &&\n        (e.key === 'ArrowLeft'");
  const moveGate = roomSrc.indexOf("const keys = ['ArrowLeft'");
  assert.ok(replayGate > -1, '回放键前置门在');
  assert.ok(moveGate > -1, '移动键分栏在');
  assert.ok(replayGate < moveGate, '回放键必须先于移动键分栏——否则 Esc 落进观察键分支永远退不出回放');
  // 回放控件聚焦不吞回放键（r_32 扩面：滑杆/选日/倍速/播放钮拖完
  // ←/→ 步进还活着——例外面从枚举控件改为 wrap.contains）；步进只吃
  // keydown——keyup 再吃一遍就是一步 10s
  assert.match(roomSrc, /rc\.wrap\.contains\(e\.target\)/, '回放控件聚焦例外在');
  assert.match(roomSrc, /down && this\.replayT >= 0/, '回放键只吃 keydown');
});

test('接线契约：表情/动作/交互随视态走（r_32.2 全量视态）', () => {
  assert.match(roomSrc, /this\.emoter\.paintShadow\(ctx, list, VS\.clock\)/,
    '回放表情泡——影子自带 em 键（记录列，idle/react/任务事件全收）');
  assert.match(roomSrc, /this\.interact\.paint\(ctx, VS\.ia, true\)/,
    '回放交互反馈——帧还原的活动表＋静音（历史不播音）');
  assert.match(roomSrc, /poseLift\(a\.pose\)/,
    '回放动作抬升读影子的 po 键（实时仪式不抬历史影子）');
  assert.match(roomSrc, /\}, this\.replayT >= 0 && a\.pose \? \{ key: a\.pose, t: a\.poseT \} : undefined\)/,
    '回放动作覆盖层——同一条像素路径，entry 从帧还原');
  assert.match(roomSrc, /const VS = this\.replayT >= 0\s*\n\s*\? shadowRoomAt\(this\.replayBufActive\(\), this\.replayT\)\s*\n\s*: this\.liveViewState\(\);/,
    '视态源切换在——同一画笔，实时/回放只是状态源不同（构造性一致）');
  assert.match(roomSrc, /this\.printer\.paintState\(ctx, VS\.prn\)/, '打印机视态');
  assert.match(roomSrc, /this\.energy\.paintState\(ctx, VS\.en\)/, '能量面视态');
  assert.match(roomSrc, /this\.paintShowcase\(ctx, VS\.tro\)/, '荣誉墙视态');
  assert.match(roomSrc, /this\.replayRoomSnap\(\), \{/,
    '采样携带房间级视态快照（draw 链读到的状态源全进帧）');
});
