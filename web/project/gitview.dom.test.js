// gitview.dom.test.js — 两级门顶层（git 版本管理总开关）的面板钉子：
// 关闭态整面只余「已关闭」卡＋指路（开关不住本面板——设置中心 → 项
// 目；不看 repo 空态、不拉分支面/座位矩阵）；enabled 缺席（旧服务端/
// 旧档）不误判成关。事件路由不进第一层冒烟（testdom 家法）。
//
//   node --test web/project/gitview.dom.test.js

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from '../room/testdom.js';

async function bootGitView(summary) {
  dom();
  // zh 钉子：断言的中文文案全线走 t()
  globalThis.localStorage.setItem('dh.ui.prefs', JSON.stringify({ lang: 'zh' }));
  const { GitView } = await import('./gitview.js');
  fetchStub(summary); // 恒回（关态若还去拉别的面，branches 落账会证伪）
  const v = new GitView(new El('div'), {
    key: 'default',
    roomName: () => '大厅',
    toast: { show() {} },
    currentProject: () => ({ workspace: '/tmp/a' }),
  });
  await v.reload();
  return v;
}

test('总开关关闭：整面只余「已关闭」卡＋指路，不再拉分支面', async () => {
  const v = await bootGitView({ enabled: false, base_branch: 'dev', auto_seat_branch: true });
  assert.ok(v.container.innerHTML.includes('git 版本管理已关闭'), '关闭卡亮出');
  assert.ok(v.container.innerHTML.includes('设置 → 项目'), '指路到设置中心');
  assert.ok(!v.container.innerHTML.includes('data-gsettings'), '面板内不再有开开关入口');
  assert.equal(v.branches, null, '关态不该去拉分支面');
  assert.ok(!v.container.innerHTML.includes('初始化仓库'), '不误亮非仓库空态');
  resetDom();
});

test('enabled 缺席（旧服务端）不误判成关——照走 repo 空态', async () => {
  const v = await bootGitView({ repo: null });
  assert.ok(v.container.innerHTML.includes('还不是 git 仓库'), '走的是非仓库空态卡');
  assert.ok(!v.container.innerHTML.includes('已关闭'), '不误亮关闭卡');
  resetDom();
});

