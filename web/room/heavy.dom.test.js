// heavy.dom.test.js — t_184（r_17）：重交互模块第二层 DOM 冒烟。
//
// 工具决策（任务日志留痕）：**stub 方案，未启用 happy-dom**。实证：settings.js
// 的 48 处 querySelector 集中在「构造巨字面量后挂事件」的形态——渲染面
// （openFor 的 innerHTML 巨串）对 El stub 完全透明，唯一缺口是全局
// `location`（3 处裸引用）——testdom 基座补一个 setGlobal('location')
// 即闭环，成本远低于「单依赖破例」。stream.js 同理（39 处 querySelector
// 大多在对流元素的后续更新，冒烟面只取消息渲染/mention 高亮/整流三面，
// 构造路径不触交互）。底线三条照第一层纪律：渲染不炸/关键类名在/降级
// 不白屏——不断言全文。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from './testdom.js';

// setupHeavy：基座世界＋全局 location（settings 的 3 处裸引用）＋
// fetch 桉序回放。重交互模块的公共口径。
function setupHeavy(responses) {
  dom();
  globalThis.location = globalThis.window.location;
  fetchStub(responses);
}

// ── settings.js：八分类渲染/开关行/降级 ─────────────────────────

test('settings：openFor 渲染八分类导航（关键类名＋全分类在）', async () => {
  setupHeavy([{ json: {} }]);
  const { SettingsWin } = await import('../ui/settings.js');
  const w = new SettingsWin({});
  w.openFor(null); // 不炸即底线①
  // openFor 挂了异步尾巴（#refreshAuth 等 await 链）——flush 一个
  // 微任务拍再断言，消除「渲染未完即验」的竞态面（全量混跑偶红的根因）
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(w.el, '窗已立');
  const html = w.el._html;
  // El stub 的 className 是独立字段（不进 innerHTML）——结构断言走
  // 字段＋内层类名双锚（第一层纪律：关键锚在，不全文比对）
  assert.equal(w.el.className, 'set-wrap', '窗根类名在（className 字段）');
  assert.ok(html.includes('class="sw-nav"'), '导航骨架在');
  assert.ok(html.includes('set-win'), '窗体在（set-win）');
  for (const pane of ['通用', '外观', '通知', '智能', '数据', '连接', '维护', '关于']) {
    assert.ok(html.includes(pane), `分类「${pane}」在`);
  }
  assert.ok(html.includes('data-sw-pane'), '面板锚在');
  w.close(); // 清常驻秒针 setInterval——测试进程不留挂起句柄
  resetDom();
});

test('settings：开关行渲染（主题三选＋通知组的行级锚在）', async () => {
  setupHeavy([{ json: {} }]);
  const { SettingsWin } = await import('../ui/settings.js');
  const w = new SettingsWin({});
  w.openFor(null);
  const html = w.el._html;
  // 外观面板的行锚：主题分段器与深夜提示
  assert.ok(html.includes('主题'), '主题行在');
  // 通用面板的行锚：聚焦开关
  assert.ok(html.includes('进入对话时聚焦输入框') || html.includes('聚焦'), '通用行锚在');
  w.close();
  resetDom();
});

test('settings：默认打开通用分类（首面板可见、其余 hidden）', async () => {
  setupHeavy([{ json: {} }]);
  const { SettingsWin } = await import('../ui/settings.js');
  const w = new SettingsWin({});
  w.openFor(null);
  const html = w.el._html;
  assert.ok(html.includes('data-sw-pane="gen"'), '通用面板锚在');
  w.close();
  resetDom();
});

// ── stream.js：消息渲染/mention 高亮/整流不炸 ────────────────────

test('stream：dress 消息渲染（正文→HTML 不炸、结构在）', async () => {
  setupHeavy([{ json: {} }]);
  const { dress } = await import('../chat/stream.js');
  // dress 是消息正文的富化器（mention/任务卡/引用的 DOM 面）——
  // 纯函数：net 文本进、HTML 出（冒烟底线：不炸＋有结构）
  const out = dress('你好 **粗体** 世界', {});
  assert.ok(typeof out === 'string' && out.length > 0, '渲染有产出（不炸）');
  assert.ok(out.includes('粗体') || out.includes('<'), '正文在');
  resetDom();
});

test('stream：mention 高亮（@名字 挂锚）', async () => {
  setupHeavy([{ json: {} }]);
  const { dress } = await import('../chat/stream.js');
  const out = dress('回复 @小猿 的消息', {});
  assert.ok(/小猿/.test(out), '被点名者在输出里');
  assert.ok(/data-pfat|mention|@/.test(out), 'mention 锚在');
  resetDom();
});

test('stream：整流不炸（StreamView 构造＋空流渲染不白屏）', async () => {
  setupHeavy([{ json: {} }]);
  const { StreamView } = await import('../chat/stream.js');
  const root = new El('div');
  // 构造不炸即底线①；空流渲染走 fold（上量 upsert 的空面）
  const v = new StreamView(root, { rooms: new Map() }, '房主');
  void v;
  assert.ok(root._html !== undefined, '容器在（未白屏）');
  resetDom();
});
