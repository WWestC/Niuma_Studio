// sfx.test.js — 场景音效契约（r_08 跑法内的 r_09 面，t_147 自检）：
// 八枚 URI 解码为合法 WAV（RIFF/PCM/11025Hz/8bit/mono）＋键名/冷却/
// 端口语义。sound.js 的依赖链含 DOM（settings→feedback→document），
// 测试走 sfxport（零 DOM）与对 sound.js 的**静态文本**断言（不 import）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { sfx, bindSfx } from './sfxport.js';

const SCENE_KEYS = ['scene-print', 'scene-pour', 'scene-crunch', 'scene-sweep',
  'scene-ding', 'scene-pop', 'scene-whisper', 'scene-binlap'];

// 从 sound.js 源文本提取 SCENE_*_URI 的 base64（不 import——DOM 链）
const soundSrc = readFileSync(new URL('../ui/sound.js', import.meta.url), 'utf8');

test('八枚场景键在 SOURCES 表中（静态断言，DOM 链隔离）', () => {
  for (const k of SCENE_KEYS) {
    assert.ok(soundSrc.includes(`'${k}'`), `SOURCES 缺 ${k}`);
  }
});

test('冷却窗常量合法（0 < cd ≤ 10s）且与定稿 §二一致', () => {
  const m = soundSrc.match(/const COOLDOWN = \{([\s\S]*?)\};/);
  assert.ok(m, 'COOLDOWN 表存在');
  const want = { 'scene-pop': 1500, 'scene-ding': 2000, 'scene-print': 2500,
    'scene-pour': 3000, 'scene-binlap': 3000, 'scene-crunch': 4000,
    'scene-sweep': 5000, 'scene-whisper': 8000 };
  for (const [k, ms] of Object.entries(want)) {
    assert.ok(soundSrc.includes(`'${k}': ${ms}`), `${k} 冷却 ${ms} 与定稿不符`);
    assert.ok(ms > 0 && ms <= 10000, `${k} 冷却越界`);
  }
});

test('氛围音独立闸在位：sceneSound 只静 scene- 前缀，消息音不陪葬', () => {
  // 闸挂在 isScene 前缀判定上（与深夜静音同判式），总闸 notifySound 仍管全部
  assert.ok(soundSrc.includes('isScene && prefs().sceneSound === false'),
    'sound.js 丢了 sceneSound 独立闸——关氛围音会连消息叮咚一起静');
  // 设置面的接线钉：行与开关写入都在（删行/删接线必红）
  const st = readFileSync(new URL('../ui/settings.js', import.meta.url), 'utf8');
  assert.ok(st.includes('data-st-scene'), '设置卡丢了「办公室氛围音」行');
  assert.ok(st.includes("setPref('sceneSound'"), '开关没写 sceneSound 偏好');
});

test('SCENE_VOLUME=0.6 系数在位（比消息音再轻四成）', () => {
  assert.ok(soundSrc.includes('SCENE_VOLUME = 0.6'), '系数缺失');
});

test('深夜静音路径在位（h < 6 场景音 return false）', () => {
  assert.ok(/isScene.*h < 6/.test(soundSrc.replace(/\n/g, ' ')), '深夜静音分支缺失');
});

test('八枚 URI 均为合法 WAV（RIFF/WAVE/PCM/11025Hz/8bit/mono 解码断言）', () => {
  // 从源文本抽每枚 URI 的 base64 段（'...' + 拼接），解码验头
  const b64 = new Map();
  for (const key of ['print', 'pour', 'crunch', 'sweep', 'ding', 'pop', 'whisper', 'binlap']) {
    const re = new RegExp(`const SCENE_${key.toUpperCase()}_URI =\\n((?:\\s*'[^']+'\\+?\\n?)+)`, '');
    const m = soundSrc.match(re);
    assert.ok(m, `SCENE_${key.toUpperCase()}_URI 存在`);
    const parts = [...m[1].matchAll(/'([^']+)'/g)].map((x) => x[1]);
    b64.set(key, parts.join(''));
  }
  for (const [key, data] of b64) {
    assert.ok(data.startsWith('data:audio/wav;base64,'), `${key} URI 前缀`);
    const raw = Buffer.from(data.slice('data:audio/wav;base64,'.length), 'base64');
    assert.equal(raw.slice(0, 4).toString(), 'RIFF', `${key} RIFF 头`);
    assert.equal(raw.slice(8, 12).toString(), 'WAVE', `${key} WAVE`);
    assert.equal(raw[20] | (raw[21] << 8), 1, `${key} PCM`);
    assert.equal(raw.readUInt32LE(24), 11025, `${key} 采样率`);
    assert.equal(raw[34], 8, `${key} 8bit`);
    assert.equal(raw[22] | (raw[23] << 8), 1, `${key} mono`);
  }
});

test('端口语义：未 bind 静默、bind 后透传、抛错吞掉（garnish 不报错）', () => {
  // 未 bind：静默（无异常即过）
  sfx('scene-print');
  // bind 透传
  let got = null;
  bindSfx((kind, opts) => { got = [kind, opts]; });
  sfx('scene-ding', { force: true });
  assert.deepEqual(got, ['scene-ding', { force: true }]);
  // bind 抛错被吞
  bindSfx(() => { throw new Error('audio stack dead'); });
  sfx('scene-pop'); // 不炸即过
  // 还原未 bind 态（不影响其他测试）
  bindSfx(null);
});

// 补钉（t_149 破坏性抽验发现）：枚举与接线点一一对应——每枚至少被一个
// room 模块真调用（删接线必须红：实验删 printer 的 scene-print 时 6 项仍绿）
test('每枚场景音至少一个 Ritual 接线引用（删线必红）', () => {
  const files = ['printer.js', 'emote.js', 'energy.js', 'cleaner.js',
    'interact.js', 'roomview.js', 'social.js'];
  const corpus = files.map((f) =>
    readFileSync(new URL(`./${f}`, import.meta.url), 'utf8')).join('\n');
  for (const k of SCENE_KEYS) {
    assert.ok(corpus.includes(`sfx('${k}')`),
      `${k} 没有任何 Ritual 接线（r_09 §二映射表）`);
  }
});
