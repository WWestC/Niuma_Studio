// bubbles.js — the speech bubbles: one bubble's measure (say → wrapped
// lines with the 汇报/↩ prefixes), the collision-avoiding layout above
// the actors (seated bubbles sit on the desk row instead), and the
// pixel-art painter with pop-in, fade-out and the stacked tails. Owns
// the bubble tier palette of palette.go's values.

import { clamp, inRect, rounded, withAlpha } from './util.js';
import { PLATE_BAND } from './plates.js';
import { plainifyMD } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

const BUBBLE_LIFE = 6;       // seconds
const BUBBLE_PAD = 4;
const BUBBLE_LINE = 18;      // 14px bubble face
const SEATED_BUBBLE_W = 126; // 1.5× the desk pitch (t_77)
const POP_DUR = 0.12, POP_RISE = 6;
const FONT = '13px system-ui, "PingFang SC", sans-serif';

const CLR = {
  goldMention: '#fff0cd',
  bubbleChat: '#fffdf5',
  bubbleReport: '#d6e8fa',
  bubbleMirror: '#ede7d9',
  bubbleEdge: '#282828',
  bubbleText: '#1a1a1a',
};

// CJK-safe greedy wrap: break after any CJK char, keep ASCII words
// whole; the last allowed line gains a rune-safe ellipsis.
export function wrapText(ctx, text, maxW, maxLines) {
  const isCJK = (ch) => /[\u3000-\u9fff\uff00-\uffef]/.test(ch);
  const lines = [];
  let line = '';
  for (const ch of text) {
    if (ctx.measureText(line + ch).width > maxW && line) {
      lines.push(line);
      line = ch;
      if (lines.length === maxLines) break;
    } else {
      line += ch;
    }
    void isCJK;
  }
  if (lines.length < maxLines && line) lines.push(line);
  if (lines.length === maxLines) {
    // overflow: the last line gets the ellipsis treatment
    let last = lines[maxLines - 1];
    if (ctx.measureText(last).width > maxW) {
      while (last.length > 1 && ctx.measureText(last + '…').width > maxW) {
        last = [...last].slice(0, -1).join('');
      }
      lines[maxLines - 1] = last + '…';
    }
  }
  return lines.filter((l) => l.length > 0);
}

// 头顶小气泡的净文本预告 plainifyMD 已搬到 ui/dom.js（与 snipOf 同居，
// 引用条摘要共用同一份剥法）——这边只消费，语法表注释在那边。

/**
 * Measure one spoken line into a bubble record (ttl fresh, rect unset —
 * the layout pass owns geometry). Seated speakers get the narrow
 * two-line treatment so the bubble reads with the desk.
 * @param {CanvasRenderingContext2D} ctx measuring context (font set here)
 * @param {{text: string, from: string, ts?: number, report?: boolean,
 *          mention?: boolean, mirror?: boolean, seated?: boolean}} src
 * @param {number} viewW
 */
export function makeBubble(ctx, src, viewW) {
  ctx.font = FONT;
  let wrapW = clamp(viewW * 0.30, 192, 400);
  let maxLines = 4;
  if (src.seated) { wrapW = Math.min(wrapW, SEATED_BUBBLE_W); maxLines = 2; }
  // 多行正文按换行切段再逐段折行——wrapText 只管宽度，\n 在 canvas
  // measureText 里是零宽字符，不切就会把整段糊成一行。空行不占泡（几行
  // 的预览里空白是浪费）；总行数到顶且后面还有内容时末行补省略号。
  const maxW = wrapW - BUBBLE_PAD * 2;
  const lines = [];
  let leftover = false;
  for (const seg of (plainifyMD(src.text) || src.text).split('\n')) {
    if (!seg.trim()) continue;
    if (lines.length >= maxLines) { leftover = true; break; }
    for (const l of wrapText(ctx, seg, maxW, maxLines - lines.length)) lines.push(l);
  }
  if (lines.length === maxLines && leftover && !lines[maxLines - 1].endsWith('…')) {
    let last = lines[maxLines - 1];
    while (last.length > 1 && ctx.measureText(last + '…').width > maxW) {
      last = [...last].slice(0, -1).join('');
    }
    lines[maxLines - 1] = last + '…';
  }
  if (lines.length && (src.report || src.mirror)) lines[0] = `${src.report ? t('汇报') : '↩'} ${lines[0]}`;
  let bw = 0;
  for (const l of lines) bw = Math.max(bw, ctx.measureText(l).width);
  return {
    lines, w: bw + BUBBLE_PAD * 2,
    h: lines.length * BUBBLE_LINE + BUBBLE_PAD * 2 - 2,
    ttl: BUBBLE_LIFE,
    report: !!src.report, mention: !!src.mention && !src.report, mirror: !!src.mirror && !src.report,
    from: src.from, raw: src.text, ts: src.ts || 0, rect: null,
  };
}

/**
 * Place every actor's live bubble, left-to-right, stacking upward on
 * overlap, always clearing the nameplate band over the head (PLATE_BAND);
 * seated bubbles anchor to their desk row instead. Returns the
 * placed [{a, rect}] the painter (and hit tests) consume; the bubble's
 * own rect field is set as a side effect.
 * @param {Iterable<import('./actor.js').Actor>} actors
 * @param {object|null} geom art geometry (avatarH, desks) @param {number} viewW
 */
export function layoutBubbles(actors, geom, viewW) {
  const speakers = [...actors].filter((a) => a.bubble)
    .sort((a, b) => a.x - b.x);
  const placed = [];
  for (const a of speakers) {
    const b = a.bubble;
    const bodyH = Math.ceil(b.h);
    let tailH = 4;
    let bx = clamp(a.x - b.w / 2, 2, viewW - Math.ceil(b.w) - 2);
    let by = a.y - (geom ? geom.avatarH : 48) - PLATE_BAND - bodyH - tailH - 2;
    const seated = a.working && a.deskIdx >= 0;
    const d = seated && geom && a.deskIdx < geom.desks.length
      ? geom.desks[a.deskIdx] : null;
    if (d && d.face === 0) {
      // camera-facing row: anchor above the island's head line, the
      // desk-row anchor (narrow bubble reads with the desk)
      by = d.y + 1 - bodyH - 2 - PLATE_BAND + (a.deskIdx % 2) * 3;
      tailH = 2;
    } else {
      // away-facing sitters and walkers float overhead, stacking on
      // collision
      let moved = true;
      while (moved) {
        moved = false;
        for (const p of placed) {
          const o = p.rect;
          while (bx < o.x + o.w && o.x < bx + b.w && by < o.y + o.h && o.y < by + bodyH + tailH) {
            by = o.y - (bodyH + tailH) - 2;
            moved = true;
          }
        }
      }
      if (by < 2) by = 2;
    }
    b.rect = { x: bx, y: by, w: Math.ceil(b.w), h: bodyH + tailH };
    placed.push({ a, rect: b.rect });
  }
  return placed;
}

/**
 * Paint the placed bubbles: pop-in rise, fade-out, the tier fill
 * (chat / 汇报 blue / mention gold / mirror parchment), white-edged on
 * hover, with the descending tail stack.
 * @param {CanvasRenderingContext2D} ctx
 * @param {{a: import('./actor.js').Actor, rect: object}[]} placed
 * @param {{on: boolean, x: number, y: number}} hover
 */
export function drawBubbles(ctx, placed, hover) {
  ctx.font = FONT;
  for (const { a } of placed) {
    const b = a.bubble;
    const { x: bx, y: by } = b.rect;
    const bodyH = Math.ceil(b.h);
    let fade = 1;
    if (b.ttl < 0.4) fade = b.ttl / 0.4;
    const age = BUBBLE_LIFE - b.ttl;
    if (age < POP_DUR) fade *= age / POP_DUR;
    const ry = by + (age < POP_DUR ? POP_RISE * (1 - age / POP_DUR) : 0);
    const hovered = hover.on && inRect(hover.x, hover.y, b.rect);
    const edge = withAlpha(hovered ? '#ffffff' : CLR.bubbleEdge, fade, hovered ? 0.92 : 1);
    const fillC = b.report ? CLR.bubbleReport : b.mention ? CLR.goldMention : b.mirror ? CLR.bubbleMirror : CLR.bubbleChat;
    const fill = withAlpha(fillC, fade, 1);
    const textC = withAlpha(CLR.bubbleText, fade, 1);
    const iw = Math.ceil(b.w);

    rounded(ctx, bx, ry, iw, bodyH, 4, edge);
    rounded(ctx, bx + 1, ry + 1, iw - 2, bodyH - 2, 3, fill);

    const seated = a.working && a.deskIdx >= 0;
    const cx = clamp(a.x, bx + 4, bx + iw - 4) - bx;
    const tails = seated ? 2 : 3;
    for (let i = 0; i < tails; i++) {
      const dw = [7, 5, 3][i], off = [0, 1, 2][i];
      ctx.fillStyle = edge;
      ctx.fillRect(bx + cx - Math.floor(dw / 2) - off, ry + bodyH + i, dw, 1);
    }
    for (let i = 0; i < tails; i++) {
      const dw = [5, 3, 1][i], off = [0, 1, 2][i];
      ctx.fillStyle = fill;
      ctx.fillRect(bx + cx - Math.floor(dw / 2) - off, ry + bodyH + i, dw, 1);
    }

    ctx.fillStyle = textC;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    let ty = ry + BUBBLE_PAD - 1;
    for (const line of b.lines) {
      ctx.fillText(line, bx + b.w / 2, ty);
      ty += BUBBLE_LINE;
    }
    ctx.textAlign = 'left';
  }
}
