#!/usr/bin/env node
// tag_cross.go2js.mjs — t_139（r_36/p_55）双向对账脚本的 Go→JS 半边。
// 输入：tools/wireaudit/wire-go-baseline.json（Go 侧基准面，go_baseline.py 生成）
// 输出：每个 struct json tag 的前端消费状态三档（确认消费/别名消费/零消费）
// ——零消费档才是真死字段候选（frame.Project 死字段的先例形态）。
// 误报预处理（评审枚举）：解构别名（const {a: b} = ...）走「tag 原名＋
// 常见别名两轮」；模板串拼接不在此面（HTTP 面）。
//
// 用法：node tools/wireaudit/tag_cross.go2js.mjs

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const baseline = JSON.parse(readFileSync('tools/wireaudit/wire-go-baseline.json', 'utf8'));

// 前端源全集（web/ 下 .js，排除 *.test.js 与 node_modules）
const files = [];
(function walk(dir) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.endsWith('.test.js') || name.startsWith('.')) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p);
    else if (name.endsWith('.js')) files.push(p);
  }
})('web');
const corpus = files.map((f) => ({ f, src: readFileSync(f, 'utf8') }));

// 点访问正则：tag 原名（.field / .field?）＋解构别名（field: / field,
const report = {};
for (const [key, st] of Object.entries(baseline.structs)) {
  const rows = [];
  for (const fld of st.fields) {
    const tag = fld.json;
    // 直接点访问（含可选链）
    const direct = corpus.filter(({ src }) =>
      new RegExp(`\\.${tag}\\b`).test(src)).map(({ f }) => f);
    // 解构形态（{ tag } 或 { tag: alias }）
    const destructure = corpus.filter(({ src }) =>
      new RegExp(`\\{[^}]*\\b${tag}\\s*[:,}]`).test(src)).map(({ f }) => f);
    const consumers = [...new Set([...direct, ...destructure])];
    rows.push({
      json: tag, unit: fld.unit || '',
      consumers: consumers.length,
      sample: consumers.slice(0, 3),
      status: consumers.length ? 'consumed' : 'ZERO-CONSUMPTION（死字段候选）',
    });
  }
  report[key] = { go: st.go, fields: rows };
}

console.log(JSON.stringify(report, null, 1));
const zero = Object.values(report).flatMap((st) => st.fields.filter((f) => f.consumers === 0));
console.error(`\n== 零消费字段（死字段候选，需人工定性）==`);
for (const z of zero) console.error(`  ${z.json} ${z.unit ? '(' + z.unit + ')' : ''}`);
console.error(`共 ${zero.length} 个零消费 / ${Object.values(report).flatMap((s) => s.fields).length} 个字段`);
