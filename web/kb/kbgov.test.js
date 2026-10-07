// kbgov.test.js — v3 治理的纯逻辑钉：文档列表文件树（docTree）、按办公
// 室隔离的架过滤（docsInScope）与编年史键形/历史卷清单（chronicleDocKey/
// volumeList）。渲染层不进测试面（DOM）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { docTree, docsInScope, splitShelves } from './kb.js';
import { volumeList, chronicleDocKey, isUnfiled } from './chronicle.js';

test('chronicleDocKey：Niuma_Studio 留 v1 的 ops/chronicle，项目各一本 p/<key>/chronicle', () => {
  assert.equal(chronicleDocKey('default'), 'ops/chronicle', 'Niuma_Studio 键（LobbyKey）');
  assert.equal(chronicleDocKey(''), 'ops/chronicle', '空键读作 Niuma_Studio（台账读侧默认）');
  assert.equal(chronicleDocKey(undefined), 'ops/chronicle');
  assert.equal(chronicleDocKey('book'), 'p/book/chronicle', '项目键');
  // 卷前缀跟志键走：项目的卷留在自己的名下，别家的卷不混进来
  assert.equal(chronicleDocKey('book') + '-vol-', 'p/book/chronicle-vol-');
});

test('docsInScope：Niuma_Studio 是独立一间房（ops/ 私档＋公共），项目看公共＋自己的 p/<key>/（别家与 ops/ 不进）', () => {
  const docs = [
    { key: 'roles/hr' },
    { key: 'ops/chronicle' },
    { key: 'ops/meetings/r_99-1' },
    { key: 'design/r31-parking' },
    { key: 'readme' },
    { key: 'p/book/establishment' },
    { key: 'p/book/chronicle' },
    { key: 'p/novel/spec' },
  ];
  assert.deepEqual(docsInScope(docs, 'default').map((d) => d.key),
    ['roles/hr', 'ops/chronicle', 'ops/meetings/r_99-1', 'design/r31-parking', 'readme'],
    'Niuma_Studio＝一切非 p/ 键（ops/ 是它自己的私档；p/default/ 防御性同侧，别家 p/ 不进）');
  assert.deepEqual(docsInScope(docs, 'book').map((d) => d.key),
    ['roles/hr', 'design/r31-parking', 'readme', 'p/book/establishment', 'p/book/chronicle'],
    '项目＝公共架（不含 Niuma_Studio ops/ 私档）＋本房 p/<key>/ 架');
  assert.deepEqual(docsInScope(docs, 'novel').map((d) => d.key),
    ['roles/hr', 'design/r31-parking', 'readme', 'p/novel/spec']);
  assert.deepEqual(docsInScope(docs, '').map((d) => d.key).length, 5, '空键读作 Niuma_Studio');
  assert.deepEqual(docsInScope(null, 'book'), [], '空表不炸');
  // p/default/ 的防御位：大厅键在 p/ 下也归大厅（正常不该出现）
  assert.deepEqual(docsInScope([{ key: 'p/default/x' }, { key: 'p/book/y' }], 'default')
    .map((d) => d.key), ['p/default/x']);
});

test('splitShelves：项目办公室两层切分（own 在前 pub 在后，pub 不含 ops/），Niuma_Studio 不切', () => {
  const docs = [
    { key: 'roles/hr' },
    { key: 'ops/chronicle' },
    { key: 'p/book/establishment' },
    { key: 'readme' },
    { key: 'p/novel/spec' },
  ];
  const book = splitShelves(docs, 'book');
  assert.deepEqual(book.own.map((d) => d.key), ['p/book/establishment'], '本房私架');
  assert.deepEqual(book.pub.map((d) => d.key), ['roles/hr', 'readme'], '公共架（别家 p/novel/ 与 Niuma_Studio ops/ 都不进）');
  const lobby = splitShelves(docs, 'default');
  assert.deepEqual(lobby.own.map((d) => d.key), ['roles/hr', 'ops/chronicle', 'readme'], 'Niuma_Studio 单架全量落 own（含自己的 ops/）');
  assert.deepEqual(lobby.pub, [], 'Niuma_Studio 不切第二层');
  const empty = splitShelves(null, 'book');
  assert.deepEqual(empty.own, [], '空表不炸');
  assert.deepEqual(empty.pub, []);
});

test('docTree 按斜杠路径归文件夹、根键留根、同层保序、计数含嵌套', () => {
  const docs = [
    { key: 'design/r15-assign' },
    { key: 'ops/chronicle' },
    { key: 'design/r14-stall' },
    { key: 'readme' },
    { key: 'roles/hr' },
    { key: 'design/sub/deep' },
  ];
  const tree = docTree(docs);
  assert.deepEqual([...tree.dirs.keys()], ['design', 'ops', 'roles'],
    '文件夹按首见次序');
  assert.deepEqual(tree.docs.map((d) => d.key), ['readme'], '无斜杠根键留根');
  const design = tree.dirs.get('design');
  assert.deepEqual(design.docs.map((d) => d.key), ['design/r15-assign', 'design/r14-stall'],
    '文件夹内保持传入顺序');
  assert.deepEqual([...design.dirs.keys()], ['sub'], '二级文件夹嵌套');
  assert.equal(design.dirs.get('sub').docs[0].key, 'design/sub/deep');
  assert.equal(design.count, 3, '文件夹计数含嵌套文档');
  assert.equal(tree.count, 6);
});

test('docTree 空表与空入参不炸', () => {
  assert.equal(docTree([]).count, 0);
  assert.equal(docTree([]).dirs.size, 0);
  assert.equal(docTree(null).count, 0);
  assert.equal(docTree(undefined).docs.length, 0);
});

test('isUnfiled：404 是未建档（空态），其余错误不是', () => {
  assert.equal(isUnfiled({ status: 404 }), true, '404＝未建档');
  assert.equal(isUnfiled(Object.assign(new Error('x: HTTP 404'), { status: 404 })), true);
  assert.equal(isUnfiled({ status: 500 }), false, '500 是真错误');
  assert.equal(isUnfiled(new Error('network')), false, '无状态码的网络错误');
  assert.equal(isUnfiled(null), false, '空错误不炸');
});

test('volumeList 过滤前缀、剔除乱号、新卷在前', () => {  const metas = [
    { key: 'ops/chronicle-vol-001' },
    { key: 'ops/chronicle-vol-003' },
    { key: 'ops/chronicle-vol-002' },
    { key: 'ops/chronicle-vol-x' }, // 非数字尾：剔除
    { key: 'ops/room-log-vol-001' }, // 别家的卷：前缀不符
    { key: 'design/r01-scene' }, // 普通归档文档：前缀不符
  ];
  const vols = volumeList(metas, 'ops/chronicle-vol-');
  assert.deepEqual(vols.map((v) => v.n), [3, 2, 1], '按卷号降序');
  assert.equal(vols[0].key, 'ops/chronicle-vol-003');
  assert.deepEqual(volumeList(null, 'ops/chronicle-vol-'), []);
});
