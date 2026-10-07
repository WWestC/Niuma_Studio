// indent.test.js — 输入井行缩进（ZCode 式）的纯函数契约：→ 无选区垫
// 光标处/有选区盖行整块垫一层，← 盖行剥最多一层前导空白（两空格或一
// 个 Tab），全无可剥返 null（← 小钮灰掉/按键不动作的同一判据）。选区
// 归宿：行级操作后改盖整块——连点 ← 恰好剥平连点 →。

import test from 'node:test';
import assert from 'node:assert/strict';
import { indentLines } from './indent.js';

test('无选区的 →：光标处垫两空格，光标落空格后（编辑器 Tab 的直觉）', () => {
  assert.deepEqual(indentLines('abc', 1, 1, 1), { value: 'a  bc', start: 3, end: 3 });
  assert.deepEqual(indentLines('', 0, 0, 1), { value: '  ', start: 2, end: 2 });
});

test('无选区的 ←：剥本行行首一层（空格与 Tab 都收），无可剥返 null', () => {
  assert.deepEqual(indentLines('  x', 3, 3, -1), { value: 'x', start: 0, end: 1 });
  assert.deepEqual(indentLines('\ty', 2, 2, -1), { value: 'y', start: 0, end: 1 });
  assert.equal(indentLines('x', 1, 1, -1), null);
});

test('多行选区 →：盖到的每行行首各垫一层，选区改盖整块', () => {
  // 'a\nb\nc' 选区 [2,5) 盖住 b、c 两行
  assert.deepEqual(indentLines('a\nb\nc', 2, 5, 1),
    { value: 'a\n  b\n  c', start: 2, end: 9 });
  // 选区从首行中段起、只搭到末行中段：两行整行都算（盖到就算）
  assert.deepEqual(indentLines('abc\ndef\nghi', 2, 5, 1),
    { value: '  abc\n  def\nghi', start: 0, end: 11 });
});

test('反向拖选（start>end）照常：按 min/max 归一后同一套语义', () => {
  assert.deepEqual(indentLines('a\nb', 3, 0, 1), { value: '  a\n  b', start: 0, end: 7 });
});

test('← 只剥盖行里有前导空白的行，其余行原样（不全有才整体不动）', () => {
  assert.deepEqual(indentLines('a\n  b', 0, 5, -1), { value: 'a\nb', start: 0, end: 3 });
});

test('连剥恰好剥平连垫：四空格两层、三空格剥二剩一', () => {
  let r = indentLines('    x', 5, 5, -1);
  assert.deepEqual(r, { value: '  x', start: 0, end: 3 });
  r = indentLines(r.value, r.end, r.end, -1);
  assert.deepEqual(r, { value: 'x', start: 0, end: 1 });
  assert.equal(indentLines(r.value, r.end, r.end, -1), null, '剥平后再 ← 不动作');
  assert.deepEqual(indentLines('   x', 4, 4, -1), { value: ' x', start: 0, end: 2 });
});
