// filelight.js — 文件浏览器的轻量语法高亮（零依赖，转义优先）：一台手
// 扫描器吃通用词法（块/行注释、字符串（含三引号）、数字、标识符），
// 每语言一张配置表（关键词集等）；不在表上的语言退化为纯转义——预览
// 永远有，颜色是加分项。与 markdown.js 同一纪律：一切输出先经 esc()，
// 源码里的原始 HTML 永远按字面显示；跨行 token（块注释/三引号串）按行
// 拆开时在每行重开同一 span，行号沟由消费方（files.js）拼。

import { esc } from '../ui/dom.js';

// 关键词表按家族共享：核心子集即可——这是预览高亮，不是补全引擎。
const KW = {
  js: 'abstract as async await break case catch class const continue debugger default delete do else enum export extends false finally for from function get if implements import in instanceof interface let new null of private protected public readonly return set static super switch this throw true try type typeof var void while with yield',
  go: 'break case chan const continue default defer else fallthrough false for func go goto if import interface map package range return select struct switch true type var nil append cap close complex copy delete imag len make new panic print println real recover',
  c: 'alignas alignof auto bool break case catch char class const constexpr continue decltype default delete do double else enum explicit export extern false float for friend goto if inline int long mutable namespace new noexcept nullptr operator private protected public register return short signed sizeof static struct switch template this throw true try typedef typename union unsigned using virtual void volatile while',
  py: 'and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return True try while with yield self',
  rust: 'as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while',
  java: 'abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for goto if implements import instanceof int interface long native new null package private protected public return short static strictfp super switch synchronized this throw throws transient true try void volatile while var record',
  ruby: 'BEGIN END alias and begin break case class def defined? do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield require',
  sql: 'select from where insert into values update set delete create table drop alter add index view join left right inner outer on as and or not null primary key foreign references default distinct group by order having limit offset union all exists between like in is case when then else end count sum avg min max',
  sh: 'if then else elif fi for while do done case esac function return in export local source echo cd ls cp mv rm mkdir cat grep sed awk curl sudo',
  lua: 'and break do else elseif end false for function goto if in local nil not or repeat return then true until while',
};

const COMMON_CONSTS = new Set(['true', 'false', 'null', 'nil', 'None', 'True', 'False', 'undefined', 'NaN', 'Infinity']);

/** 语言配置：line=行注释起点；block=[开,合]；str=引号字符；triple=三引
 *  号；kw=关键词集（小写比对）；markup=HTML/XML 标签词法。 */
function cfg(ext, name) {
  const kw = (s) => new Set(s.split(' '));
  const byName = {
    dockerfile: { line: ['#'], str: ['"', "'"], kw: kw(KW.sh) },
    makefile: { line: ['#'], str: ['"', "'"], kw: kw(KW.sh) },
  };
  if (name && byName[name.toLowerCase()]) return byName[name.toLowerCase()];
  const byExt = {
    '.js': KW.js, '.mjs': KW.js, '.cjs': KW.js, '.jsx': KW.js,
    '.ts': KW.js, '.tsx': KW.js, '.json': KW.js,
    '.go': KW.go,
    '.c': KW.c, '.h': KW.c, '.cc': KW.c, '.cpp': KW.c, '.hpp': KW.c, '.cxx': KW.c,
    '.py': KW.py, '.pyw': KW.py,
    '.rs': KW.rust,
    '.java': KW.java, '.kt': KW.java,
    '.rb': KW.ruby,
    '.sql': KW.sql,
    '.sh': KW.sh, '.bash': KW.sh, '.zsh': KW.sh,
    '.lua': KW.lua,
    '.css': null, '.scss': null,
    '.yaml': null, '.yml': null, '.toml': null, '.ini': null, '.conf': null, '.env': null, '.properties': null,
    '.html': null, '.htm': null, '.xml': null, '.svg': null, '.vue': null,
  };
  if (!(ext in byExt)) return null;
  if (ext === '.json') {
    return { line: ['//'], block: [['/*', '*/']], str: ['"', "'", '`'], constsOnly: true, kw: kw(KW.js) };
  }
  if (ext === '.css' || ext === '.scss') {
    return { block: [['/*', '*/']], str: ['"', "'"], kw: new Set() };
  }
  if (['.html', '.htm', '.xml', '.svg', '.vue'].includes(ext)) {
    return { markup: true, block: [['<!--', '-->']], str: ['"', "'"], kw: new Set() };
  }
  if (['.yaml', '.yml', '.toml', '.ini', '.conf', '.env', '.properties'].includes(ext)) {
    return { line: ['#', ';'], str: ['"', "'"], kw: new Set() };
  }
  const py = ext === '.py' || ext === '.pyw';
  return {
    line: py ? ['#'] : ['//'],
    block: py ? [] : ext === '.lua' ? [['--[[', ']]']] : [['/*', '*/']],
    str: py ? ['"', "'"] : ['"', "'", '`'],
    triple: py ? ['"""', "'''"] : [],
    kw: kw(byExt[ext]),
  };
}

/** ext/name → 语言配置（供 Viewer 判断；null=纯文本降级）。 */
export function langOf(ext, name) { return cfg(ext || '', name || ''); }

/**
 * 把一段源码高亮成逐行 HTML（消费方拼行号沟）。每行都是安全的转义
 * 文本＋span；跨行 token 在每行重开同色 span。langKey 传 langOf 的
 * 返回值或 null。
 * @param {string} code 源码（\r 先行剥掉）
 * @param {{line?: string[], block?: [string,string][], str?: string[], triple?: string[], kw?: Set<string>, constsOnly?: boolean}|null} lang
 * @returns {string[]} 每行一段 HTML
 */
export function highlightLines(code, lang) {
  const src = code.replace(/\r/g, '');
  const lines = [''];
  const push = (cls, text) => {
    const parts = String(text).split('\n');
    for (let i = 0; i < parts.length; i++) {
      if (i > 0) lines.push('');
      if (parts[i] === '') continue;
      lines[lines.length - 1] += cls
        ? `<span class="${cls}">${esc(parts[i])}</span>`
        : esc(parts[i]);
    }
  };
  if (!lang) { // 纯文本：只转义＋拆行（push 自带按行拆分）
    push(null, src);
    return lines;
  }
  const isStr = (ch) => (lang.str || []).includes(ch);
  let i = 0;
  const n = src.length;
  let plain = '';
  const flush = () => { if (plain) { push(null, plain); plain = ''; } };
  while (i < n) {
    const rest = src.slice(i, i + 3);
    // 三引号串（py docstring）
    if ((lang.triple || []).some((q) => rest.startsWith(q))) {
      const q = (lang.triple || []).find((t) => rest.startsWith(t));
      const end = src.indexOf(q, i + q.length);
      const stop = end < 0 ? n : end + q.length;
      flush();
      push('tok-s', src.slice(i, stop));
      i = stop;
      continue;
    }
    // 块注释
    const bp = (lang.block || []).find(([o]) => src.startsWith(o, i));
    if (bp) {
      const end = src.indexOf(bp[1], i + bp[0].length);
      const stop = end < 0 ? n : end + bp[1].length;
      flush();
      push('tok-c', src.slice(i, stop));
      i = stop;
      continue;
    }
    // 行注释（吃到行尾）
    const lp = (lang.line || []).find((o) => src.startsWith(o, i));
    if (lp) {
      let end = src.indexOf('\n', i);
      if (end < 0) end = n;
      flush();
      push('tok-c', src.slice(i, end));
      i = end;
      continue;
    }
    const ch = src[i];
    // 标记词法（html/xml）：<标签名/</> 上色，属性字符串照走字符串腿
    if (lang.markup && ch === '<') {
      const m = /^<\/?[A-Za-z!][\w:.-]*/.exec(src.slice(i));
      if (m) {
        flush();
        push('tok-k', m[0]);
        i += m[0].length;
        continue;
      }
    }
    // 字符串：引号开头，反斜杠转义跳过
    if (isStr(ch)) {
      let j = i + 1;
      while (j < n) {
        if (src[j] === '\\') { j += 2; continue; }
        if (src[j] === ch || src[j] === '\n') { if (src[j] === ch) j++; break; }
        j++;
      }
      flush();
      push('tok-s', src.slice(i, j));
      i = j;
      continue;
    }
    // 数字
    if (/[0-9]/.test(ch) && !/[A-Za-z0-9_$]/.test(src[i - 1] || '')) {
      const m = /^(?:0[xXbBoO][0-9a-fA-F_]+|[0-9][0-9_]*(?:\.[0-9_]+)?(?:[eE][+-]?[0-9]+)?)[a-zA-Z%]*/.exec(src.slice(i));
      flush();
      push('tok-n', m ? m[0] : ch);
      i += m ? m[0].length : 1;
      continue;
    }
    // 标识符：关键词/常量/函数名
    if (/[A-Za-z_$]/.test(ch)) {
      const m = /^[A-Za-z_$][A-Za-z0-9_$]*/.exec(src.slice(i));
      const word = m ? m[0] : ch;
      const after = src.slice(i + word.length).match(/^\s*\(/);
      let cls = null;
      if (lang.kw?.has(word)) cls = 'tok-k';
      else if (COMMON_CONSTS.has(word)) cls = 'tok-v';
      else if (after && !lang.constsOnly) cls = 'tok-f';
      if (cls) {
        flush();
        push(cls, word);
      } else {
        plain += word;
      }
      i += word.length;
      continue;
    }
    plain += ch;
    i++;
  }
  flush();
  return lines;
}
