// livefeed.test.js — t_197：办公室效果门（回放帧不播）钉行为面。
// 载史/断线补窗重放的 say/report 若照播效果，整窗历史的对话气泡＋表情
// 泡＋啵声会在同一帧齐放（「效果集中蹦出来」的根因）——三层接线各钉
// 一道：
//   ① 门本体（roomview.sayEffectsLive 纯函数）：回放帧沉默、直播帧放行；
//   ② frame() 的 Say/Report 分支必须走这道门（RoomView 构造要 canvas
//      2d，无 DOM 测试基建——接线面照 guide.test.js 先例源扫描）；
//   ③ 送帧链两端（rooms.js #intake 出参、app.js onLiveFrame 入参）：
//      backfill 标志逐跳透传，中途丢参＝门被绕过、齐放复发。
// 任一处被删/漂移，这里必红。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dom, resetDom } from './testdom.js';

test('门本体：回放帧沉默，直播 say/report 放行，其他帧类型不归它管', async () => {
  dom();
  const { sayEffectsLive } = await import('./roomview.js');
  const { Msg } = await import('../wire/wire.js');
  const say = { type: Msg.Say, from: '小狐', text: '搞定～' };
  const report = { type: Msg.Report, from: '小鹿', text: '工作汇报' };
  assert.equal(sayEffectsLive(say, false), true, '直播 say 播');
  assert.equal(sayEffectsLive(report, undefined), true, '直播 report 播（live 路不带第三参）');
  assert.equal(sayEffectsLive(say, true), false, '载史/补窗重放的 say 沉默');
  assert.equal(sayEffectsLive(report, true), false, '重放的 report 沉默');
  assert.equal(sayEffectsLive({ type: Msg.Task, event: 'created' }, false), false,
    '任务帧不归这道门管（printer/emote 各分支自有 !backfill 守卫）');
  resetDom();
});

test('frame() 的 Say/Report 分支走门：回放不再直呼 this.say', () => {
  const src = readFileSync(new URL('./roomview.js', import.meta.url), 'utf8');
  assert.match(src, /if \(sayEffectsLive\(f, backfill\)\) \{\s*this\.say\(/,
    'Say/Report 效果分支必须经 sayEffectsLive 把门');
  assert.equal((src.match(/this\.say\(f\.from/g) || []).length, 1,
    'frame() 里 this.say(f.from 只许门后一处——旁路即齐放复发');
});

test('送帧链两端：backfill 逐跳透传，中途丢参就是门被绕过', () => {
  const rooms = readFileSync(new URL('../chat/rooms.js', import.meta.url), 'utf8');
  const app = readFileSync(new URL('../app.js', import.meta.url), 'utf8');
  assert.match(rooms, /onLiveFrame\?\.\(frame, room\.key, fromBackfill\)/,
    '#intake 必须把回放标志交给 onLiveFrame（回调契约第三参）');
  assert.match(app, /roomView\?\.frame\(f, key, backfill\)/,
    'app.js 必须把 backfill 透传给办公室视图');
  assert.match(app, /pluginBus\.emitChat\(f, key, backfill\)/,
    'app.js 必须把 backfill 透传给插件总线');
});
