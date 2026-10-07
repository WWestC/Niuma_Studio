// chronicle.test.js — t_153（r_10）：编年史解析器的纯逻辑钉子。
// parseChronicle 的四段式条目/日期分组/stray 收集/体例节跳过——渲染
// 层不进测试面（DOM），降级路径由空数据断言覆盖。

import test from 'node:test';
import assert from 'node:assert/strict';
import { parseChronicle } from './chronicle.js';

const MD = `# 工作室志（编年史）

体例说明（t_150 定稿 v1.0）——条目格式如下：
\`\`\`
YYYY-MM-DD｜类型｜人｜一句话
\`\`\`

2026-10-01｜里程碑｜小牛、小马｜r_01 三点功能立顶：茶水间＋回工位＋物品交互
2026-10-01｜事件｜小鹿｜茶水间房主首验不通过，出施工单
2026-10-02｜上岗｜小狐｜前端开发·扩编补员到岗
（回溯补录批次说明：本批 25 条覆盖 r_01–r_09）
2026-10-02｜里程碑｜小狐、小猿｜r_04 表现力三部曲实现：三引擎上线
这不是条目的一行正文
2026-10-02｜漏格式的｜条目
`;

test('四段式条目逐行解析成日期分组', () => {
  const { dates } = parseChronicle(MD);
  assert.equal(dates.length, 2, '两天分组');
  assert.equal(dates[0].date, '2026-10-01');
  assert.equal(dates[0].items.length, 2);
  assert.equal(dates[1].date, '2026-10-02');
  assert.equal(dates[1].items.length, 2);
});

test('字段拆解：类型/人/一句话各归其位', () => {
  const { dates } = parseChronicle(MD);
  const it = dates[1].items[0];
  assert.equal(it.type, '上岗');
  assert.equal(it.who, '小狐');
  assert.equal(it.text, '前端开发·扩编补员到岗');
});

test('体例节与非条目行不进时间线；漏格式条目进 stray 不丢', () => {
  const { dates, stray } = parseChronicle(MD);
  const all = dates.flatMap(d => d.items.map(i => i.text));
  assert.ok(!all.some(t => t.includes('条目格式')), '体例节文字不进条目');
  assert.ok(!all.some(t => t.includes('YYYY')), '格式模板行不进条目');
  assert.ok(stray.some(s => s.includes('漏格式的')), '漏格式条目收进 stray');
  assert.ok(!stray.some(s => s.startsWith('#')), '标题行不进 stray');
  // 批次说明行不含 ｜ 分隔——静默跳过（体例节内容）
  assert.ok(!stray.some(s => s.includes('回溯补录')), '批次说明不进 stray');
});

test('空正文与全非条目正文都安全（降级前提）', () => {
  assert.deepEqual(parseChronicle('').dates, []);
  assert.deepEqual(parseChronicle('随便一段\n没有条目').dates, []);
  const { dates, stray } = parseChronicle('随便一段｜有竖线但非四段');
  assert.equal(dates.length, 0);
  assert.equal(stray.length, 1, '含 ｜ 的未识别行进 stray');
});

// 按办公室隔离的接线钉：面板 load(root, key) 读喂进来的那本志——URL
// 即合同（大厅 ops/chronicle、项目 p/<key>/chronicle），卷前缀跟志键走。
// fetch 手写记录器按 URL 分发（fetchStub 只按序不认 URL，这里要认）。
test('面板读喂入的志键：Niuma_Studio 本/项目本互不串，卷前缀跟键走', async () => {
  const { dom, resetDom, El } = await import('../room/testdom.js');
  dom();
  const calls = [];
  globalThis.fetch = async (url) => {
    calls.push(String(url));
    const body = String(url).includes('p/book/chronicle')
      ? '2026-10-03｜交付｜小鹿｜项目志条目'
      : '2026-10-03｜交付｜小猿｜Niuma_Studio 志条目';
    const cfg = String(url).includes('archived=1') ? { json: [] } : { json: { body } };
    return {
      ok: true, status: 200,
      headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
      json: async () => cfg.json,
    };
  };
  const { ChroniclePanel } = await import('./chronicle.js');
  const p = new ChroniclePanel();

  await p.load(new El('div'), 'p/book/chronicle');
  assert.ok(calls.some((u) => u.includes('/kb/docs/p/book/chronicle')),
    '项目房读 p/<key>/chronicle');
  assert.ok(p.root.innerHTML.includes('项目志条目'), '渲染的是项目本条目');

  await p.load(new El('div'), 'ops/chronicle');
  assert.ok(calls.some((u) => u.includes('/kb/docs/ops/chronicle')),
    'Niuma_Studio 读 ops/chronicle');
  assert.ok(p.root.innerHTML.includes('Niuma_Studio 志条目'), '渲染的是 Niuma_Studio 本条目');
  assert.ok(!p.root.innerHTML.includes('项目志条目'), '别家条目不串台');
  resetDom();
});
