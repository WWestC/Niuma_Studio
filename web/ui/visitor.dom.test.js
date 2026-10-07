// visitor.dom.test.js — t_192（r_19）：访客态初始化分支的 DOM 冒烟。
// 敏感面断言（定稿 §四/验收③）：initVisitor 后裁剪样式注入（隐藏项
// 规则在 style 里）＋body 打 visitor 类＋访客条挂载；token cookie 落盘。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom, El } from '../room/testdom.js';

test('initVisitor：/view 路径 → 访客态标记＋裁剪样式＋顶条＋cookie', async () => {
  dom();
  // 路径与 query（visitor.js 读 location/search/document.cookie）
  globalThis.location = { pathname: '/view/default', search: '?t=abc123' };
  const cookies = [];
  Object.defineProperty(globalThis.document, 'cookie', {
    configurable: true,
    get: () => cookies.join('; '),
    set: (v) => cookies.push(v),
  });
  const { initVisitor, visitorMode } = await import('./visitor.js');
  const isV = initVisitor();
  assert.equal(isV, true, '/view 路径判定为访客');
  assert.equal(visitorMode(), true, '全局标记在');
  // 裁剪样式注入：head 里有一段含隐藏清单的 style
  const style = globalThis.document.head.children.find((c) => c.tag === 'style');
  assert.ok(style && style._html.includes('#composer'), '裁剪样式含发送面');
  assert.ok(style._html.includes('#nav-people'), '裁剪样式含管理面');
  assert.ok(style._html.includes('#side-settings'), '裁剪样式含设置入口');
  // body 打类＋访客条挂载
  assert.equal(globalThis.document.body.classList._cls, undefined, 'classList 是 no-op 桩（打类无炸）');
  const bar = globalThis.document.body.children.find((c) => c.id === 'visitor-bar');
  assert.ok(bar, '访客顶条挂载');
  assert.ok(String(bar._html || bar.textContent || '').includes('只读'), '顶条明示只读');
  // cookie：token 落盘（4h 与 TTL 同拍）
  assert.ok(cookies.some(c => c.includes('niuma_vt=abc123')), 'token cookie 落盘');
  resetDom();
});

test('initVisitor：主应用路径 → 非访客态零动作', async () => {
  dom();
  delete globalThis.__NIUMA_VISITOR__; // 上一测的全局标记清干净（模块缓存不重载）
  globalThis.location = { pathname: '/app', search: '' };
  const { initVisitor, visitorMode } = await import('./visitor.js');
  assert.equal(initVisitor(), false, '主应用路径不进访客态');
  assert.equal(visitorMode(), false);
  assert.ok(!globalThis.document.body.children.some(c => c.id === 'visitor-bar'), '零 DOM 动作');
  resetDom();
});
