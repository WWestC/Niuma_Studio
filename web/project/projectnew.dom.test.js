// projectnew.dom.test.js — 立项表单 v2.13 的须知与预检确认门：
// 创建态常驻「立项须知」（编辑态不带）；提交先打 POST /projects/
// preflight——error（服务端必拒项）内联拦下、不发创建；warn（注意
// 事项）第一次点只亮清单＋改键「已了解风险，继续立项」，再点一次才
// 发 POST；预检面打不通不拦人（正门仍由服务端把关）。
//
// El stub 的 querySelector 每次都回新节点，表单交互（注入值/读按钮文
// 本）需要按选择器持久化——本文件在原型上打一层记忆补丁（仅本文件
// 的进程内生效，node --test 每文件独立进程）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom, El } from '../room/testdom.js';

const origQS = El.prototype.querySelector;
const nodeCache = new WeakMap();
El.prototype.querySelector = function (sel) {
  if (!nodeCache.has(this)) nodeCache.set(this, {});
  const c = nodeCache.get(this);
  if (!(sel in c)) c[sel] = origQS.call(this, sel);
  return c[sel];
};

function resp(body, ok = true, status = 200) {
  return {
    ok, status,
    headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
    json: async () => body,
    text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
  };
}

// 按端点分流：/projects/preflight 与 POST /projects 都要记里程
function routeFetch({ preflight, createOk = true } = {}) {
  const calls = [];
  globalThis.fetch = async (url, init = {}) => {
    const u = String(url);
    const method = init.method || 'GET';
    calls.push({ url: u, method });
    if (u.includes('/projects/preflight')) {
      return preflight ? resp(preflight) : resp({ error: 'down' }, false, 500);
    }
    if (method === 'POST' && u.endsWith('/projects')) {
      return createOk ? resp({ key: 'alpha', name: '甲', status: 'draft' }) : resp({ error: 'boom' }, false, 400);
    }
    return resp({});
  };
  return calls;
}

async function makeForm({ project = null, submitted = [] } = {}) {
  const { ProjectNewForm } = await import('./projectnew.js');
  return new ProjectNewForm({
    project,
    onSubmit: async (payload) => { submitted.push(payload); },
  });
}

function fillCreate(form, { key = 'alpha', name = '甲项目', workspace = '/tmp/alpha' } = {}) {
  const el = form.el;
  el.querySelector('[data-key]').value = key;
  el.querySelector('[data-name]').value = name;
  el.querySelector('[data-workspace]').value = workspace;
}

test('创建态常驻立项须知；编辑态不带', async () => {
  dom();
  const f1 = await makeForm();
  const html1 = globalThis.document.body.children.at(-1)._html;
  assert.ok(html1.includes('立项须知'), '创建态有须知盒');
  assert.ok(html1.includes('会话出生地'), '工作区风险第一条在');
  assert.ok(html1.includes('归档后另立新项目'), '锁定与迁移口径在');
  assert.ok(html1.includes('data-risks'), '预检清单槽在');
  const f2 = await makeForm({ project: { key: 'alpha', name: '甲', status: 'active', workspace: '/tmp/a' } });
  const html2 = globalThis.document.body.children.at(-1)._html;
  assert.ok(!html2.includes('立项须知'), '编辑态不带须知');
  f1.close();
  f2.close();
  resetDom();
});

test('预检 error（服务端必拒项）内联拦下，不发创建', async () => {
  dom();
  const calls = routeFetch({ preflight: { blocking: true, findings: [
    { level: 'error', code: 'ws_root', text: 'workspace 不能是文件系统根目录（/）' },
  ] } });
  const submitted = [];
  const form = await makeForm({ submitted });
  fillCreate(form, { workspace: '/' });
  await form.submit();
  assert.ok(calls.some((c) => c.url.includes('/projects/preflight')), '预检已打');
  assert.ok(!calls.some((c) => c.method === 'POST' && c.url.endsWith('/projects')), 'error 拦下不发创建');
  assert.equal(submitted.length, 0, 'onSubmit 未被调');
  const status = form.el.querySelector('[data-status]');
  assert.ok(status._html.includes('根目录'), '拒绝理由内联亮出');
  form.close();
  resetDom();
});

test('预检 warn 第一次点亮清单等确认，再点一次才创建', async () => {
  dom();
  const calls = routeFetch({ preflight: { blocking: false, findings: [
    { level: 'warn', code: 'ws_missing', text: '目录还不存在——调度器不会代建工作区' },
  ] } });
  const submitted = [];
  const form = await makeForm({ submitted });
  fillCreate(form, { workspace: '/tmp/not-yet' });
  await form.submit();
  assert.ok(!calls.some((c) => c.method === 'POST' && c.url.endsWith('/projects')), '第一次点不放行');
  const risks = form.el.querySelector('[data-risks]');
  assert.equal(risks.hidden, false, '风险清单已亮');
  assert.ok(risks._html.includes('目录还不存在'), 'warn 文案在清单里');
  const okBtn = form.el.querySelector('[data-ok]');
  assert.equal(okBtn.textContent, '已了解风险，继续立项', '确认改键');
  await form.submit();
  assert.equal(submitted.length, 1, '确认后 onSubmit 放行一次');
  assert.equal(form.el, null, '成功后表单收起');
  resetDom();
});

test('预检面打不通不拦人：直走正门（服务端仍把关）', async () => {
  dom();
  const calls = routeFetch({ preflight: null });
  const submitted = [];
  const form = await makeForm({ submitted });
  fillCreate(form);
  await form.submit();
  assert.ok(calls.some((c) => c.url.includes('/projects/preflight')), '预检已尝试');
  assert.equal(submitted.length, 1, '预检失效不拦创建');
  resetDom();
});

// ── Git 引导段（v2.14）──

test('Git 引导段：创建态三件（自动 init/基线/座位自动建枝）＋总开关，编辑态只带总开关', async () => {
  dom();
  routeFetch({});
  const f1 = await makeForm();
  const html1 = globalThis.document.body.children.at(-1)._html;
  assert.ok(html1.includes('Git 引导'), '引导段标题在');
  assert.ok(html1.includes('data-git-init') && html1.includes('data-git-base') && html1.includes('data-git-seat'), '三件控件在');
  assert.ok(html1.includes('data-git-on'), '总开关在');
  assert.ok(html1.includes('分支工作法'), '工作法须知在');
  const f2 = await makeForm({ project: { key: 'alpha', name: '甲', status: 'active', workspace: '/tmp/a' } });
  const html2 = globalThis.document.body.children.at(-1)._html;
  assert.ok(!html2.includes('data-git-init'), '编辑态不带引导段');
  assert.ok(html2.includes('data-git-on'), '编辑态带总开关镜像');
  f1.close();
  f2.close();
  resetDom();
});

test('提交载荷带 git_plan：勾选与基线原样上送', async () => {
  dom();
  routeFetch({ preflight: { blocking: false, findings: [] } });
  const submitted = [];
  const form = await makeForm({ submitted });
  fillCreate(form);
  // stub 不解析模板里的 checked 属性，四件显式设位（浏览器里总开关与座位档默认勾）
  form.el.querySelector('[data-git-on]').checked = true;
  form.el.querySelector('[data-git-init]').checked = true;
  form.el.querySelector('[data-git-seat]').checked = true;
  form.el.querySelector('[data-git-base]').value = 'dev';
  await form.submit();
  assert.equal(submitted.length, 1);
  assert.deepEqual(submitted[0].git_plan, { init_if_missing: true, base_branch: 'dev', auto_seat_branch: true });
  assert.equal(submitted[0].git_disabled, false, '总开关开着→git_disabled=false');
  resetDom();
});

test('总开关关掉：创建载荷 git_disabled=true 且引导计划置空；编辑载荷带镜像', async () => {
  dom();
  routeFetch({ preflight: { blocking: false, findings: [] } });
  const submitted = [];
  const form = await makeForm({ submitted });
  fillCreate(form);
  form.el.querySelector('[data-git-on]').checked = false;
  form.el.querySelector('[data-git-init]').checked = true; // 细项勾了也不算数
  await form.submit();
  assert.equal(submitted.length, 1);
  assert.equal(submitted[0].git_disabled, true, '关掉→git_disabled=true');
  assert.equal(submitted[0].git_plan, null, '引导计划随总开关置空');

  const submitted2 = [];
  const form2 = await makeForm({ project: { key: 'alpha', name: '甲', status: 'active', workspace: '/tmp/a' }, submitted: submitted2 });
  form2.el.querySelector('[data-name]').value = '甲';
  form2.el.querySelector('[data-git-on]').checked = false; // 编辑态镜像关掉
  await form2.submit();
  assert.equal(submitted2.length, 1);
  assert.equal(submitted2[0].git_disabled, true, '编辑载荷带总开关镜像');
  resetDom();
});

test('probeWorkspace：有仓自动填基线＋亮检测语；外层仓库整段停用', async () => {
  dom();
  routeFetch({ preflight: { blocking: false, findings: [], git: { is_repo: true, branch: 'main', base_fill: 'main', owns_repo: true } } });
  const f1 = await makeForm();
  fillCreate(f1, { workspace: '/tmp/repo-ws' });
  await f1.probeWorkspace();
  assert.equal(f1.el.querySelector('[data-git-base]').value, 'main', '基线自动填当前分支');
  assert.ok(f1.el.querySelector('[data-git-state]')._html.includes('检测到'), '检测语亮出');
  f1.close();
  routeFetch({ preflight: { blocking: false, findings: [], git: { is_repo: true, outer_repo: true } } });
  const f2 = await makeForm();
  fillCreate(f2, { workspace: '/tmp/outer/sub' });
  await f2.probeWorkspace();
  assert.ok(f2.el.querySelector('[data-git-state]')._html.includes('外层仓库'), '外层仓库警示亮出');
  assert.equal(f2.el.querySelector('[data-git-init]').disabled, true, '自动 init 停用');
  assert.equal(f2.el.querySelector('[data-git-seat]').disabled, true, '座位自动建枝停用');
  f2.close();
  resetDom();
});
