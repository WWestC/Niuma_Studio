// emote.test.js — 表情系统的纯逻辑面（r_08，t_141）：关键词映射表全键
// 在 E1–E12 集合内（键名冲突事故的回归钉）、emoji 直配、正则不误伤。
// node --test web/room/ 即跑（node ≥20 原生 runner，零第三方依赖）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { EMOJIS, EMOJI_TO_EMOTE, emoteFromText } from './emote.js';

// E1–E12 全集（视觉分册 rev 4 终稿键名——键名唯一真源）
const VALID = new Set(['happy', 'thinking', 'working', 'done', 'confused',
  'sleepy', 'coffee', 'sweat', 'angry', 'love', 'star', 'wave']);

test('EMOJIS 十二款齐全且键都在 E1–E12 全集内', () => {
  assert.equal(Object.keys(EMOJIS).length, 12, '恰好 12 款（不扩不缺）');
  for (const key of Object.keys(EMOJIS)) {
    assert.ok(VALID.has(key), `键 ${key} 不在 E1–E12 集合（键名冲突回归）`);
  }
});

test('每款图标有画法与 emoji 映射', () => {
  for (const [key, v] of Object.entries(EMOJIS)) {
    assert.equal(typeof v.draw, 'function', `${key}.draw`);
    assert.equal(typeof v.emoji, 'string', `${key}.emoji`);
    assert.ok(v.emoji.length > 0, `${key}.emoji 非空`);
  }
});

test('EMOJI_TO_EMOTE 是 12 枚有像素对应的直配（宁缺毋滥口径）', () => {
  assert.equal(EMOJI_TO_EMOTE.size, 12);
  for (const key of EMOJI_TO_EMOTE.values()) {
    assert.ok(VALID.has(key), `emoji 映射到外键 ${key}`);
  }
});

test('emoteFromText 关键词命中', () => {
  const cases = [
    ['谢谢房主', 'happy'],
    ['哈哈有趣', 'happy'],
    ['让我想想', 'thinking'],
    ['为什么不行？', 'confused'],
    ['困了 zzz', 'sleepy'],
    ['去喝杯咖啡', 'coffee'],
    ['出 bug 了', 'sweat'],
    ['气死了', 'angry'],
    ['我很喜欢这个', 'love'],
    ['大家好', 'wave'],
    ['欢迎新同事', 'wave'],
  ];
  for (const [text, want] of cases) {
    assert.equal(emoteFromText(text), want, `"${text}" 应命中 ${want}`);
  }
});

test('emoteFromText emoji 直配（😊→happy、❤️→love）', () => {
  assert.equal(emoteFromText('这个方案 😊'), 'happy');
  assert.equal(emoteFromText('支持 ❤️'), 'love');
});

test('emoteFromText 无关文本不冒泡（宁缺毋滥）', () => {
  assert.equal(emoteFromText('收到，已处理'), null);
  assert.equal(emoteFromText(''), null);
  assert.equal(emoteFromText(null), null);
});

test('emoteFromText 词内 @ 不误伤（「bug@小策」关键词照常，无 emoji 不冒）', () => {
  // 「bug」是 sweat 关键词——词内 @ 的提及边界不影响关键词面
  assert.equal(emoteFromText('这个 bug@小猿 看看'), 'sweat');
});
