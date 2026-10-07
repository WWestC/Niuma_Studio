// files.dom.test.js — 文件板块（FilesBoard，#/files 一级板块）的 DOM 冒
// 烟：进门跟房落位（getProjects→跟房→树根）、项目选择器、树行、查看器
// 三态（文本高亮/md 渲染/图片/二进制）、检索盒、同项目刷新不打扰树。
// testdom 的 El 是「innerHTML 字符串槽」型假节点——树行断言走
// view.els.tree（querySelector 惰性回新节点，行都铺在它的 innerHTML 里）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from '../room/testdom.js';

const PROJECTS = [
  { key: 'default', name: 'Niuma_Studio', status: 'active' },
  { key: 'alpha', name: '项目甲', status: 'active' },
];
const ROOT = {
  project: 'alpha', path: '', parent: '', workspace: '/tmp/alpha',
  entries: [
    { name: 'web', dir: true },
    { name: 'app.py', dir: false, size: 1234 },
    { name: 'Readme.md', dir: false, size: 88 },
  ],
};

async function boot(payloads, current = 'alpha') {
  const { FilesBoard } = await import('./files.js');
  fetchStub(payloads);
  const book = { current };
  const toast = { show() {} };
  const v = new FilesBoard(new El('div'), book, toast);
  await v.revealed();
  return v;
}

// 点击事件桩：target.closest 按选择器回行桩（真 El 的 closest 恒 null）。
function clickRow(dataset, closestSel = '[data-file]') {
  return { target: { closest: (sel) => (sel === closestSel ? { dataset, closest: () => null } : null) } };
}

// 按 URL 分发的 fetch 桩（fetchStub 是按序队列，树刷新的并发拉取用路
// 由式好断言）：命中 match 的回 200 body，否则 404。
function fetchRoute(routes) {
  globalThis.fetch = async (input) => {
    const url = String(input);
    for (const r of routes) {
      if (r.match(url)) {
        return { ok: true, status: 200, headers: { get: () => 'application/json' }, json: async () => r.body };
      }
    }
    return { ok: false, status: 404, headers: { get: () => null }, json: async () => ({}) };
  };
}

// ① 进门跟房：人在 alpha 房，板块落 alpha 的工作区（不是大厅缺省）。
test('FilesBoard：进门跟房落位＋根清单渲染', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT]);
  assert.equal(v.key, 'alpha', '跟房落位 alpha');
  assert.ok(v.root.innerHTML.includes('fs-wrap'), '双栏壳在');
  assert.ok(v.root.innerHTML.includes('data-fs-search'), '检索盒在');
  assert.ok(v.els.proj.innerHTML.includes('value="alpha"'), '选择器含 alpha');
  const tree = v.els.tree.innerHTML;
  assert.ok(tree.includes('data-dir="web"'), '目录行在');
  assert.ok(tree.includes('data-file="app.py"'), '文件行在');
  assert.ok(/fs-fext[^>]*>py</.test(tree), 'py 徽标在');
  assert.ok(v.els.body.innerHTML.includes('选一个文件看内容'), '空态提示在');
  resetDom();
});

// ② 大厅缺省：book.current=default 跟房落大厅（＝Niuma_Studio 仓）。
test('FilesBoard：Niuma_Studio 缺省看 Niuma_Studio', async () => {
  dom();
  const v = await boot([PROJECTS, { ...ROOT, project: 'default', workspace: '/Users/x/Niuma_Studio' }], 'default');
  assert.equal(v.key, 'default');
  resetDom();
});

// ③ 文本文件：行号沟＋高亮 token＋头栏元数据。
test('FilesBoard：文本查看器（行号＋高亮＋头栏）', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'app.py', name: 'app.py', ext: '.py',
    size: 30, kind: 'text', content: 'def f():\n    return None\n',
  }]);
  v.onClick(clickRow({ file: 'app.py' }));
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(v.els.head.innerHTML.includes('app.py'), '头栏文件名');
  assert.ok(!v.els.head.innerHTML.includes('/tmp'), '头栏不带工作区绝对路径');
  assert.ok(v.els.body.innerHTML.includes('fs-ln'), '行号沟在');
  assert.ok(v.els.body.innerHTML.includes('tok-k'), '关键词上色');
  resetDom();
});

// ④ md：默认源码（VSCode 口径），「看渲染」切换 markdown.js 渲染。
test('FilesBoard：md 默认源码＋渲染切换钮', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'Readme.md', name: 'Readme.md', ext: '.md',
    size: 20, kind: 'text', content: '# 标题\n\n正文 **粗**\n',
  }]);
  v.onClick(clickRow({ file: 'Readme.md' }));
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(v.els.body.innerHTML.includes('fs-code'), '默认源码视图');
  assert.ok(v.els.head.innerHTML.includes('看渲染'), '渲染切换钮在');
  v.onClick(clickRow({}, '[data-mdmode]'));
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(v.els.body.innerHTML.includes('fs-md'), '切换后渲染容器在');
  assert.ok(v.els.body.innerHTML.includes('<h1'), '标题渲染成 h1');
  resetDom();
});

// ⑤a 空文件：明示状态卡，不是一行空白。
test('FilesBoard：空文件给明示卡', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'empty.txt', name: 'empty.txt', ext: '.txt',
    size: 0, kind: 'text', content: '',
  }]);
  v.onClick(clickRow({ file: 'empty.txt' }));
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(v.els.body.innerHTML.includes('空文件'), '空文件明示在');
  assert.ok(!v.els.body.innerHTML.includes('fs-code'), '不走代码视图');
  resetDom();
});

// ⑤b 超长单行：按字符帽截断＋帽注说明（2MB 压一行的文件不整行进 DOM）。
test('FilesBoard：超长单行截断＋帽注', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'big.oneline', name: 'big.oneline', ext: '.txt',
    size: 100000, kind: 'text', content: 'x'.repeat(100000),
  }]);
  v.onClick(clickRow({ file: 'big.oneline' }));
  await new Promise((r) => setTimeout(r, 0));
  const body = v.els.body.innerHTML;
  assert.ok(body.includes('fs-capnote'), '帽注在');
  assert.ok(body.includes('1 行超长'), '超长行计数在');
  assert.ok(body.length < 10000, '单行不整行进 DOM');
  resetDom();
});

// ⑤ 图片：走 trace/file 直出，信封不带 content。
test('FilesBoard：图片直出 trace/file', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'web/shot.png', name: 'shot.png', ext: '.png',
    size: 9000, kind: 'image',
  }]);
  v.onClick(clickRow({ file: 'web/shot.png' }));
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(v.els.body.innerHTML.includes('/p/alpha/trace/file?path='), '图片走 trace/file');
  assert.ok(v.els.body.innerHTML.includes('fs-imgbox'), '图框在');
  resetDom();
});

// ⑥ 二进制：明说不可预览。
test('FilesBoard：二进制不预览', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'a.bin', name: 'a.bin', ext: '.bin',
    size: 10, kind: 'binary',
  }]);
  v.onClick(clickRow({ file: 'a.bin' }));
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(v.els.body.innerHTML.includes('二进制文件不预览'));
  resetDom();
});

// ⑦ 检索：结果面板命中可点，标题带计数；清空收起。
test('FilesBoard：检索结果面板', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT]);
  fetchStub([{ project: 'alpha', q: 'hub', hits: ['web/chat/hub.go'], truncated: false }]);
  await v.runSearch('hub');
  assert.ok(!v.els.hits.hidden, '面板展开');
  assert.ok(v.els.hits.innerHTML.includes('1 个匹配'), '计数在');
  assert.ok(v.els.hits.innerHTML.includes('data-file="web/chat/hub.go"'), '命中可点');
  await v.runSearch('');
  assert.ok(v.els.hits.hidden, '清空收起');
  resetDom();
});

// ⑧ 同项目刷新不打扰树：reload 不再拉 /fs/tree；离场 conceal 后换房不跟。
test('FilesBoard：同项目 reload 不重拉树＋离场不跟房', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT]);
  const calls = [];
  const raw = globalThis.fetch;
  globalThis.fetch = async (...a) => { calls.push(String(a[0])); return raw(...a); };
  await v.reload();
  assert.ok(!calls.some((u) => u.includes('/fs/tree')), '树不重拉');
  assert.equal(calls.filter((u) => u.includes('/fs/file')).length, 0, '没开文件也不拉文件');
  v.conceal();
  v.book.current = 'beta';
  v.onRoomSwitch(); // 不可见：零动作
  assert.equal(v.key, 'alpha', '离场不跟房');
  resetDom();
});

// ⑨ 目录点击展开：子目录随行入册，toggleDir 不再吞点击（真浏览器实
//    录的根因：根的子目录没进 nodes 表，#toggleDir 查无节点直接 return）。
test('FilesBoard：点目录展开（子目录入册）', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'web', parent: '', workspace: '/tmp/alpha',
    entries: [{ name: 'chat', dir: true }, { name: 'app.js', dir: false, size: 710 }],
  }]);
  assert.ok(v.nodes.has('web'), '子目录随根清单入册');
  assert.equal(v.nodes.get('web').open, false, '缺省收起');
  v.onClick(clickRow({ dir: 'web' }, '[data-dir]'));
  await new Promise((r) => setTimeout(r, 0));
  const n = v.nodes.get('web');
  assert.ok(n.children?.length === 2, '子清单已拉回');
  assert.equal(n.open, true, '点击后展开');
  assert.ok(v.nodes.has('web/chat'), '孙目录随行入册');
  // 再点收起（children 已在，零请求）
  v.onClick(clickRow({ dir: 'web' }, '[data-dir]'));
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(v.nodes.get('web').open, false, '再点收起');
  resetDom();
});

// ⑪ 刷新树按钮：根层与已展开目录原地重拉——展开态保留、变动行重建、
//    磁盘上消失的行/册一并清走（VSCode 式刷新，不收走用户手里的树）。
test('FilesBoard：刷新树——展开态保留＋变动行原地重建', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT, {
    project: 'alpha', path: 'web', parent: '', workspace: '/tmp/alpha',
    entries: [{ name: 'chat', dir: true }, { name: 'app.js', dir: false, size: 710 }],
  }]);
  v.onClick(clickRow({ dir: 'web' }, '[data-dir]'));
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(v.nodes.get('web').open, true, '前置：web 已展开');
  // 磁盘演化：根多 new.py 少 app.py；web/ 少 chat 多 ui
  fetchRoute([
    { match: (u) => u.includes('/fs/tree') && u.endsWith('path='), body: { project: 'alpha', path: '', parent: '', workspace: '/tmp/alpha', entries: [{ name: 'web', dir: true }, { name: 'new.py', dir: false, size: 5 }] } },
    { match: (u) => u.includes('/fs/tree'), body: { project: 'alpha', path: 'web', parent: '', workspace: '/tmp/alpha', entries: [{ name: 'ui', dir: true }] } },
  ]);
  v.onClick(clickRow({}, '[data-fs-refresh]'));
  await new Promise((r) => setTimeout(r, 0));
  const tree = v.els.tree.innerHTML;
  assert.ok(tree.includes('new.py'), '新增文件进树');
  assert.ok(!tree.includes('app.py'), '消失文件出树');
  // 子目录行铺在惰性 querySelector 节点上（El 世界的既有口径，⑨ 同款），
  // 字符串断言够不着嵌套层——展开态与重建改验册子。
  assert.ok(v.nodes.has('web/ui'), '新目录随重建入册');
  assert.equal(v.nodes.get('web').open, true, '展开态保留');
  assert.ok(!v.nodes.has('web/chat'), '消失目录出册');
  resetDom();
});

// ⑫ fs_dirty 自动刷新：正浏览的项目在屏才理；连发脏帧防抖合并成一拍。
test('FilesBoard：fs_dirty 防抖合并刷新＋外项目不理', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT]);
  const calls = [];
  fetchRoute([
    { match: (u) => { if (!u.includes('/fs/tree')) return false; calls.push(u); return true; }, body: ROOT },
  ]);
  v.fsDebounce = 1;
  v.onFsDirty('beta'); // 外项目：不理
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(calls.length, 0, '外项目的脏帧不拉树');
  v.onFsDirty('alpha');
  v.onFsDirty('alpha'); // 连发：合并成一拍
  await new Promise((r) => setTimeout(r, 30));
  assert.equal(calls.length, 1, '防抖合并：一拍一刷');
  resetDom();
});

// ⑬ 回场即刷新：离场再进场（同项目），树重拉——不再是上次进场的快照。
test('FilesBoard：同项目回场即刷新树', async () => {
  dom();
  const v = await boot([PROJECTS, ROOT]);
  v.conceal();
  fetchRoute([
    { match: (u) => u.includes('/projects'), body: PROJECTS },
    { match: (u) => u.includes('/fs/tree') && u.endsWith('path='), body: { project: 'alpha', path: '', parent: '', workspace: '/tmp/alpha', entries: [{ name: 'web', dir: true }, { name: 'fresh.md', dir: false, size: 3 }] } },
  ]);
  await v.revealed();
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(v.els.tree.innerHTML.includes('fresh.md'), '回场树已刷新');
  resetDom();
});

// ⑩ 接线静态断言：app.js 挂了 files 板块路由，index.html 有导航与容器，
//    访客裁剪清单盖住 #nav-files，项目管理板已无文件页签残留。
test('接线：一级板块路由/导航/容器/访客裁剪在位', async () => {
  const fs = await import('node:fs');
  const app = fs.readFileSync(new URL('../app.js', import.meta.url), 'utf8');
  assert.ok(app.includes("'files'"), 'boards 数组含 files');
  assert.ok(app.includes("new FilesBoard($('#board-files')"), '实例化');
  assert.ok(app.includes('filesBoard?.revealed()'), '路由 revealed');
  assert.ok(app.includes('filesBoard?.onRoomSwitch()'), '换房跟房钩子');
  assert.ok(app.includes('onFsDirty: (project) => filesBoard?.onFsDirty(project)'), 'fs_dirty 接线');
  const html = fs.readFileSync(new URL('../index.html', import.meta.url), 'utf8');
  const css = fs.readFileSync(new URL('../app.css', import.meta.url), 'utf8');
  assert.ok(html.includes('id="nav-files"'), '导航锚点');
  assert.ok(html.includes('id="board-files"'), '板块容器');
  for (const sel of ['.fs-wrap', '.fs-tree', '.fs-code', '.tok-k', '.fs-cline', '#board-files', '.fs-refresh']) {
    assert.ok(css.includes(sel), `CSS ${sel} 在`);
  }
  // 行号沟水平向吸附：长行横滚时行号钉在 fs-body 左缘（VSCode 式）。
  // sticky 与不透明底缺一不可——只 sticky 没底色，代码会从行号底下透出来
  assert.ok(/\.fs-ln\s*{[^}]*position:\s*sticky[^}]*left:\s*0/.test(css), '行号沟 sticky left 在');
  assert.ok(/\.fs-ln\s*{[^}]*background:\s*var\(--board\)/.test(css), '行号沟不透明底在');
  const vis = fs.readFileSync(new URL('../ui/visitor.js', import.meta.url), 'utf8');
  assert.ok(vis.includes('#nav-files'), '访客裁剪清单盖住文件板块');
  const proj = fs.readFileSync(new URL('../project/project.js', import.meta.url), 'utf8');
  assert.ok(!proj.includes("id: 'files'"), '项目管理板无文件页签残留');
});
