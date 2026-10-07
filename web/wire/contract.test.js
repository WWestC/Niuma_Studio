// contract.test.js — the JS half of the frame-contract pin: the Go wire
// package (generated into gen/frame-contract.json by
// `go run ./tools/wiregen`) is the protocol's single truth, and this
// file holds the workbench's mirror (wire.js) to it — both directions:
// a Go constant the JS mirror lacks, or a JS entry that drifted from
// the Go value, fails here with the offending name spelled out.
// Known OUT of contract (by design, asserted as such below): the three
// owner management verbs whose Go constants live in the SERVER package
// (kick/rank_set/agent_archive — pre-wire history), and the
// transport-level CloseReason / trace-kind vocabulary the browser never
// sees as data.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { Msg, Origin, Event, LobbyKey } from './wire.js';

const contract = JSON.parse(readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), 'gen', 'frame-contract.json'), 'utf8'));

// Go 常量名 → JS 镜像路径 的映射规则（wire.js 的既定命名法）。
function jsPathFor(goName) {
  if (goName === 'LobbyKey') return { root: { LobbyKey }, key: 'LobbyKey', rootName: 'LobbyKey' };
  for (const [prefix, obj] of [['Msg', Msg], ['Origin', Origin], ['Event', Event]]) {
    if (goName.startsWith(prefix) && goName.length > prefix.length) {
      return { root: obj, key: goName.slice(prefix.length), rootName: prefix };
    }
  }
  return null; // 不在前端镜像面（CloseReason/Trace* 等传输层词汇）
}

test('every Go wire constant with a JS mirror carries the identical value', () => {
  const mismatches = [];
  for (const [goName, goValue] of Object.entries(contract.frames)) {
    const p = jsPathFor(goName);
    if (!p) continue;
    const jsValue = p.root[p.key];
    if (jsValue === undefined) {
      mismatches.push(`${goName}: Go 有（"${goValue}"），wire.js 的 ${p.rootName}.${p.key} 缺失`);
    } else if (jsValue !== goValue) {
      mismatches.push(`${goName}: Go="${goValue}" JS="${jsValue}"（wire.js 的 ${p.rootName}.${p.key}）`);
    }
  }
  assert.deepEqual(mismatches, [],
    '前端帧镜像与 Go wire 契约漂移——改 wire.js 对齐，或改 wire/wire.go 后 `go run ./tools/wiregen` 重生契约：');
});

test('every mirrored JS frame entry exists on the Go side (no stale vocabulary)', () => {
  const goFrames = new Map(Object.entries(contract.frames));
  const stale = [];
  // Msg/Origin/Event 三个镜像对象逐项反查；白名单：Go 常量在 server 包的三个管理动词。
  const serverPkgVerbs = new Set(['kick', 'rank_set', 'agent_archive']);
  for (const [rootName, obj] of [['Msg', Msg], ['Origin', Origin], ['Event', Event]]) {
    for (const [key, value] of Object.entries(obj)) {
      if (serverPkgVerbs.has(value) && rootName === 'Msg') continue;
      const goName = rootName + key;
      if (!goFrames.has(goName)) {
        stale.push(`${rootName}.${key}="${value}"：Go wire 包无 ${goName}（陈旧或拼错）`);
      }
    }
  }
  assert.deepEqual(stale, [], 'wire.js 里存在 Go 契约没有的词条：');
});

test('LobbyKey stays byte-identical (the reserved room key)', () => {
  assert.equal(LobbyKey, contract.frames['LobbyKey']);
});

// ─── 载荷形状对齐（types 消费端）────────────────────────────────────────
// 契约的 types 半边（Go wire 镜像 struct 的 JSON tag 全集）此前无人消费：
// 常量值钉死了、字段形状却只能靠运行时发现。此处把 wire.js 的 JSDoc
// typedef 钉到 Go 真源上，两个方向：
//   ① 漂移向（全部配对）：typedef 写出的每个属性必须真实存在于 Go tag
//     ——JS 侧拼错、或 Go 侧改名后 JS 没跟，当场红；
//   ② 完整向（FULL_MIRRORS）：Go 加了字段而 JS 镜像没学，当场红。
//     消费面子集（Frame 只钉工作台吃的那半边）与带服务端附加字段的
//     超集（Skill.body_bytes）不参与完整向。

// Go 类型 → wire.js typedef 名 的显式配对（名字不同处是前端自己的词汇：
// Message→Frame、Image→MsgImage、Task→TaskFull、Req→Requirement、
// Meeting→MeetingRecord、Line→MeetingLine）。
const TYPE_MIRRORS = {
  Member: 'Member', Message: 'Frame', Quote: 'Quote', Image: 'MsgImage',
  TraceEntry: 'TraceEntry', Question: 'Question', QuestionAsk: 'QuestionAsk',
  QuestionOption: 'QuestionOption', Notice: 'Notice', Skill: 'Skill',
  MCPHeader: 'MCPHeader', MCPServer: 'MCPServer', Req: 'Requirement',
  PlanTask: 'PlanTask', Plan: 'Plan', Meeting: 'MeetingRecord',
  Line: 'MeetingLine', Task: 'TaskFull', KbDoc: 'KbDoc',
  Look: 'Look', LookAcc: 'LookAcc',
};

// 完整镜像（typedef 属性集 == Go tag 集）：载荷型全量投影。这十个
// wireconv 族载荷型（Task/Patch/Proposal/LogEntry/Plan/PlanTask/Merge/
// Req/Meeting/Line）的全量镜像已生成为 gen/types.js（wiregen 产物，
// 下方 gen 面测试双向钉死）；wire.js 里只留消费词汇的别名
// （TYPE_ALIASES）。仍在 wire.js 手写全量的是非 wireconv 族：
const FULL_MIRRORS = new Set([
  'Member', 'Quote', 'Image', 'TraceEntry', 'Question', 'QuestionAsk',
  'QuestionOption', 'Notice', 'MCPHeader', 'MCPServer', 'KbDoc', 'Look', 'LookAcc',
]);

// wire.js 的消费词汇别名：typedef 名 → gen/types.js 的生成名
// （全量镜像从手写 @property 换成 import() 别名——Go 加字段重跑
// wiregen 即同步，wire.js 不再逐字段跟）。
const TYPE_ALIASES = {
  TaskFull: 'Task', Plan: 'Plan', PlanTask: 'PlanTask',
  MeetingRecord: 'Meeting', MeetingLine: 'Line',
};

// 已审定的 JS 侧附加字段（存在于 typedef、不在 Go wire 镜像里——
// 服务端读面拼装的列，不是协议漂移）：Skill.body_bytes 是库列表行的
// 附加列；Frame.rank 是 rank_set 的 C->S 载荷，由 server 包组装
// （rank.go——与 kick/rank_set/agent_archive 三个 server 动词同族的
// pre-wire 历史，wire.Message 镜像不携带）。
const JS_EXTRAS = { Skill: ['body_bytes'], Message: ['rank'] };

// 解析 JSDoc：typedef 名 + extends 引用 + @property 名集。
// 属性类型可含嵌套花括号（TaskFull.log 的 {{ts,by,note}[]}），按括号
// 深度配平后再取名字。wire.js 与 gen/types.js 共用这一份解析。
const wireSrc = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), 'wire.js'), 'utf8');
function parseTypedefBlocks(src) {
  const out = new Map();
  for (const [, block] of src.matchAll(/\/\*\*([\s\S]*?)\*\//g)) {
    const head = block.match(/@typedef\s+\{([^}]*)\}\s+(\w+)/);
    if (!head) continue;
    const props = new Set();
    for (const [, entry] of block.matchAll(/@property\s+([^\n]+)/g)) {
      let s = entry.trim();
      if (s.startsWith('{')) {
        let depth = 0, i = 0;
        for (; i < s.length; i++) {
          if (s[i] === '{') depth++;
          else if (s[i] === '}') { if (--depth === 0) { i++; break; } }
        }
        s = s.slice(i);
      }
      const name = s.trim().match(/^(\[?)(\w+)\]?/);
      if (name) props.add(name[2]);
    }
    out.set(head[2], { ref: head[1] === 'Object' ? null : head[1], props });
  }
  return out;
}
const typedefs = parseTypedefBlocks(wireSrc);
// extends 并集（KbDoc = KbDocMeta + body）。
function propsOf(name, seen = new Set()) {
  const td = typedefs.get(name);
  if (!td || seen.has(name)) return new Set();
  seen.add(name);
  return new Set([...td.props, ...(td.ref ? propsOf(td.ref, seen) : [])]);
}
function goTags(goName) {
  return new Set((contract.types[goName] || []).map(f => f.tag.split(',')[0]));
}

test('the type-mirror table itself is not rotten', () => {
  const bad = [];
  for (const [goName, jsName] of Object.entries(TYPE_MIRRORS)) {
    if (!contract.types[goName]) bad.push(`Go 契约无类型 ${goName}`);
    if (!typedefs.has(jsName)) bad.push(`wire.js 无 typedef ${jsName}`);
  }
  assert.deepEqual(bad, [], 'TYPE_MIRRORS 配对表烂了（一侧已改名/删除）：');
});

test('every mirrored typedef property exists on the Go side (no invented fields)', () => {
  const bad = [];
  for (const [goName, jsName] of Object.entries(TYPE_MIRRORS)) {
    const tags = goTags(goName);
    const extras = new Set(JS_EXTRAS[goName] || []);
    for (const p of propsOf(jsName)) {
      if (!tags.has(p) && !extras.has(p)) {
        bad.push(`${goName}↔${jsName}：typedef 属性 "${p}" 不在 Go tag 里（拼错，或 Go 改名后 JS 没跟）`);
      }
    }
  }
  assert.deepEqual(bad, [], 'wire.js typedef 写出了 Go 契约没有的字段：');
});

test('full-mirror typedefs track every Go payload field (additive-only, but learned)', () => {
  const bad = [];
  for (const goName of FULL_MIRRORS) {
    const tags = goTags(goName);
    const props = propsOf(TYPE_MIRRORS[goName]);
    for (const t of tags) {
      if (!props.has(t)) {
        bad.push(`${goName}：Go 新字段 "${t}" 未进 ${TYPE_MIRRORS[goName]} 的 typedef`);
      }
    }
  }
  assert.deepEqual(bad, [], 'Go wire 镜像加了字段、wire.js 的全量镜像没学——同步 typedef：');
});

// ─── 生成面：gen/types.js（wiregen 产物）───────────────────────────────
// wireconv 族的十个载荷型全量镜像由 `go run ./tools/wiregen` 生成进
// gen/types.js（Go 侧字段类型标注随契约一起走）。双向钉：生成物每个
// 属性必须对应 Go tag，Go 每个 tag 必须被生成物学到——生成物陈旧
// （改了 Go 忘跑 wiregen）当场红，重跑即愈。
const GEN_TYPES = ['Task', 'Patch', 'Proposal', 'LogEntry', 'Plan', 'PlanTask',
  'Merge', 'Req', 'Meeting', 'Line'];

const genSrc = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), 'gen', 'types.js'), 'utf8');
const genTypedefs = parseTypedefBlocks(genSrc);

test('gen/types.js carries every payload family type, field-exact both ways', () => {
  const bad = [];
  for (const goName of GEN_TYPES) {
    const tags = goTags(goName);
    const td = genTypedefs.get(goName);
    if (!td) {
      bad.push(`gen/types.js 缺 ${goName} 的 typedef（生成物陈旧——重跑 \`go run ./tools/wiregen\`）`);
      continue;
    }
    const props = td.props;
    for (const t of tags) if (!props.has(t)) bad.push(`${goName}：Go tag "${t}" 未被生成物学到（重跑 wiregen）`);
    for (const p of props) if (!tags.has(p)) bad.push(`${goName}：生成物属性 "${p}" 不在 Go tag 里（重跑 wiregen）`);
  }
  assert.deepEqual(bad, [], 'gen/types.js 与 Go 契约漂移——`go run ./tools/wiregen` 重生并提交：');
});

test('wire.js aliases point at the right generated mirrors', () => {
  const bad = [];
  for (const [jsName, genName] of Object.entries(TYPE_ALIASES)) {
    const td = typedefs.get(jsName);
    if (!td) {
      bad.push(`wire.js 缺别名 typedef ${jsName}`);
      continue;
    }
    const want = `import('./gen/types.js').${genName}`;
    if (td.ref !== want) bad.push(`${jsName} 的别名应指 ${want}，实际 "${td.ref}"`);
    if (!genTypedefs.has(genName)) bad.push(`gen/types.js 缺 ${genName}（别名悬空）`);
  }
  assert.deepEqual(bad, [], 'wire.js 的生成镜像别名接错了：');
});

// ─── 入站信封对齐（envelope 消费端）────────────────────────────────────
// 契约的 envelope 半边（server 侧 memberEnvelope 的 JSON tag 全集——入站
// 帧在服务端的解码形状）。Frame 对出站是刻意子集（工作台只吃半边），
// 但入站槽是全量面：服务端解码一个 Frame 文档没学的槽，意味着前端在
// 发一个「文档说不出是什么」的字段——或更糟，tag 两边各拼各的、动词
// 静默 no-op。单方向钉完整向：信封每个 tag 必须被 Frame typedef 学到。
test('every envelope slot is learned by the Frame typedef (inbound is a full mirror)', () => {
  const props = propsOf('Frame');
  const bad = [];
  for (const f of contract.envelope || []) {
    const tag = f.tag.split(',')[0];
    if (!props.has(tag)) bad.push(`memberEnvelope.${f.Field}（json:${tag}）`);
  }
  assert.deepEqual(bad, [],
    '服务端入站信封有、wire.js Frame 文档没学的槽——补 @property（入站解码面是全量镜像，不吃子集纪律）：');
});
