// filelight.test.js — 文件页签的语法高亮器纯逻辑冒烟：转义优先（源码
// 里的 HTML 一律按字面显示）、跨行 token（块注释/三引号串）逐行重开
// span、语言家族配置命中与未知扩展降级。

import test from 'node:test';
import assert from 'node:assert/strict';
import { highlightLines, langOf } from './filelight.js';

// ① 安全底线：任何语言的输出都是转义文本——原始 HTML 不穿透。
test('highlightLines：源码 HTML 按字面显示（不穿透）', () => {
  for (const lang of [langOf('.go'), langOf('.py'), langOf('.js'), langOf('.html'), null]) {
    const out = highlightLines('<script>alert("x")</script>', lang);
    const joined = out.join('\n');
    assert.ok(!joined.includes('<script>'), '原文标签不得出现');
    assert.ok(joined.includes('&lt;script'), `转义形态应在（${lang ? '有语言' : '纯文本'}）`);
  }
});

// ② go 家族：关键词/字符串/数字上色，块注释跨行逐行重开 span。
test('highlightLines：go 关键词＋跨行块注释', () => {
  const go = langOf('.go');
  assert.ok(go, '.go 有配置');
  const lines = highlightLines('func main() {\n/* 注释\n第二行 */ x := "s"\n}', go);
  assert.match(lines[0], /<span class="tok-k">func<\/span>/);
  assert.match(lines[1], /<span class="tok-c">/);
  assert.match(lines[2], /^<span class="tok-c">第二行 \*\/<\/span>/);
  assert.match(lines[2], /<span class="tok-s">&quot;s&quot;<\/span>/);
  assert.equal(lines[3], '}', '末行右括号原样');
});

// ③ 字符串内转义引号不断串：\" 跳过。
test('highlightLines：转义引号不断串', () => {
  const lines = highlightLines('s := "a\\"b" + f(1)', langOf('.go'));
  assert.match(lines[0], /<span class="tok-s">&quot;a\\&quot;b&quot;<\/span>/);
  assert.match(lines[0], /<span class="tok-f">f<\/span>/);
  assert.match(lines[0], /<span class="tok-n">1<\/span>/);
});

// ④ py 三引号 docstring 跨行；# 行注释。
test('highlightLines：py 三引号＋行注释', () => {
  const py = langOf('.py');
  const lines = highlightLines('"""doc\nspan"""\n# 备注\ndef f(): pass', py);
  assert.match(lines[0], /^<span class="tok-s">&quot;&quot;&quot;doc<\/span>/);
  assert.match(lines[1], /^<span class="tok-s">span&quot;&quot;&quot;<\/span>/);
  assert.match(lines[2], /^<span class="tok-c"># 备注<\/span>/);
  assert.match(lines[3], /<span class="tok-k">def<\/span> <span class="tok-f">f<\/span>/);
});

// ⑤ 未知扩展降级纯文本（只有转义＋拆行）；Makefile 按文件名命中。
test('langOf：未知扩展 null；Makefile/Dockerfile 按名命中', () => {
  assert.equal(langOf('.zzz'), null);
  assert.equal(langOf('', 'readme'), null);
  assert.ok(langOf('', 'Makefile'));
  assert.ok(langOf('', 'Dockerfile'));
  assert.ok(langOf('.MD'.toLowerCase()) === null || langOf('.md') === null, 'md 不走高亮（走渲染）');
});

// ⑥ 标记家族：标签名 tok-k、属性字符串 tok-s、注释 tok-c。
test('highlightLines：html 标签/属性串/注释', () => {
  const html = langOf('.html');
  const lines = highlightLines('<div class="a"><!-- 注释 -->', html);
  assert.match(lines[0], /<span class="tok-k">&lt;div<\/span>/);
  assert.match(lines[0], /<span class="tok-s">&quot;a&quot;<\/span>/);
  assert.match(lines[0], /<span class="tok-c">&lt;!-- 注释 --&gt;<\/span>/);
});

// ⑦ 空串与纯换行不炸、行数对齐。
test('highlightLines：空文件与连续换行', () => {
  assert.deepEqual(highlightLines('', null), ['']);
  assert.equal(highlightLines('a\n\nb', null).length, 3);
});
