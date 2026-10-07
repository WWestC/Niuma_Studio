// indent.js — 输入井的行缩进词汇（ZCode 式）：牛马对话与小助手顾问室
// 两张输入脸共用（与 fold.js 同一纪律——纯函数、零 DOM 依赖，node:test
// 直测）。语义对齐 ZCode 的输入框：
//   →（Tab）：无选区＝光标处垫一层缩进（对齐小列、拼简单表格的本职，
//     不是把整行顶下去）；有选区＝选区盖到的每一行行首各垫一层——
//     嵌套列表就这么摞。
//   ←（Shift+Tab）：盖到的行行首剥最多一层前导空白（两空格或一个
//     Tab——手动敲进去的 Tab 也收得回来），盖行里一行都没有可剥的才
//     整体不动（null——← 小钮灰掉/按键不动作走的是同一判据）。
// 缩进单位＝两空格：正文渲染走 markdown.js 的 indentOf 按列宽算嵌套，
// 两空格是「- 」项嵌一层的最小可靠档。
// 选区归宿：行级操作后选区改盖整块（首行行首到末行行尾）——连点 ←
// 能把连点 → 恰好剥平（盖住的还是同一批行），这是选区保形的取舍。

/** 缩进一层垫什么。 */
export const INDENT_UNIT = '  ';

/**
 * 对选区盖到的行做一层缩进/退缩。
 * @param {string} value 全文
 * @param {number} start 选区起点（与 end 谁大谁小都行）
 * @param {number} end 选区终点
 * @param {1|-1} dir 1＝缩进（→/Tab），-1＝退缩（←/Shift+Tab）
 * @returns {{value: string, start: number, end: number}|null} 新全文与
 *   新选区；dir<0 且盖行全无前导空白可剥时为 null
 */
export function indentLines(value, start, end, dir) {
  const s = Math.min(start, end), e = Math.max(start, end);
  if (dir > 0 && s === e) {
    // 无选区的 →：垫在光标处，光标落空格后（编辑器 Tab 的直觉）
    return {
      value: value.slice(0, s) + INDENT_UNIT + value.slice(s),
      start: s + INDENT_UNIT.length, end: s + INDENT_UNIT.length,
    };
  }
  // 行块范围：首行从行首算起（选区只要盖到这行、哪怕只盖行尾半截，
  // 这行就算数），末行到行尾；无选区（s===e）即光标行。
  const ls = value.lastIndexOf('\n', s - 1) + 1;
  let le = value.indexOf('\n', e);
  if (le < 0) le = value.length;
  let stripped = 0; // ← 真剥掉的字数（判 null 用）
  const block = value.slice(ls, le).split('\n').map((line) => {
    if (dir > 0) return INDENT_UNIT + line;
    const m = /^(?:\t| {1,2})/.exec(line);
    if (!m) return line;
    stripped += m[0].length;
    return line.slice(m[0].length);
  }).join('\n');
  if (dir < 0 && !stripped) return null;
  return { value: value.slice(0, ls) + block + value.slice(le), start: ls, end: ls + block.length };
}
