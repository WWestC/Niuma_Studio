// loader.test.js — t_197：pluginBus.emitChat 的回放帧门——chat.watch
// 订阅面只吃直播帧。载史/断线补窗的重放若照发，计数类订阅（示例插件
// 工作室脉搏）每次开页/重连都虚胖一圈；emitChat 是插件拿对话帧的唯一
// 管道，门设这里一处即全堵。分发与退订是既有语义，顺手同钉防漂。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom } from '../room/testdom.js';

test('emitChat：直播帧分发，backfill 重放帧不分发', async () => {
  dom();
  const { pluginBus } = await import('./loader.js');
  const seen = [];
  const off = pluginBus.onChat((f, key) => seen.push([key, f.text]));
  pluginBus.emitChat({ type: 'say', text: '直播' }, 'p_1', false);
  pluginBus.emitChat({ type: 'say', text: '直播缺省' }, 'p_1'); // live 路不带第三参
  pluginBus.emitChat({ type: 'say', text: '重放' }, 'p_1', true); // 载史/补窗
  assert.deepEqual(seen, [['p_1', '直播'], ['p_1', '直播缺省']],
    '重放帧必须被门拦下，直播帧（含缺省第三参）照常到');
  off();
  pluginBus.emitChat({ type: 'say', text: '退订后' }, 'p_1', false);
  assert.equal(seen.length, 2, '退订生效');
  resetDom();
});

test('emitChat：一颗订阅回调抛错不连坐别的订阅（既有纪律顺手钉住）', async () => {
  dom();
  const { pluginBus } = await import('./loader.js');
  const seen = [];
  pluginBus.onChat(() => { throw new Error('插件自己的锅'); });
  pluginBus.onChat((f) => seen.push(f.text));
  pluginBus.emitChat({ type: 'say', text: '照常' }, 'p_1', false);
  assert.deepEqual(seen, ['照常'], '抛错订阅只落 console，不反噬房间流');
  resetDom();
});
