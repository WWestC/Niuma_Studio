// taskmeta.js — the task ledger's shared display meta (Feishu 台优化):
// the four-state machine's labels/dots, the four-level priority's
// labels/chips, the numeric-progress fallback, the derived 逾期 rule
// and the due chip — one home so the kanban, the list view, the drawer
// and the create form never drift apart. Pure display twins of the
// server's derived.go (the engine stays the truth); no protocol
// knowledge.

import { t } from '../ui/i18n.js';

/** The four lifecycle states' display meta (dot hex = the tag colors). */
export const STATUS_META = {
  todo: { label: t('待处理'), cls: 'st-todo', dot: '#8f959e' },
  doing: { label: t('进行中'), cls: 'st-doing', dot: '#1456f0' },
  done: { label: t('已完成'), cls: 'st-done', dot: '#34c724' },
  cancelled: { label: t('已取消'), cls: 'st-cancelled', dot: '#c9cdd4' },
};

/**
 * The four priority levels (飞书式紧急/高/中/低). rank drives the
 * sorts: 已设级的在前（紧急最先），未设置（rank 9）恒沉底——飞书同款。
 */
export const PRIORITY_META = {
  urgent: { label: t('紧急'), cls: 'pri-urgent', rank: 0 },
  high: { label: t('高'), cls: 'pri-high', rank: 1 },
  medium: { label: t('中'), cls: 'pri-medium', rank: 2 },
  low: { label: t('低'), cls: 'pri-low', rank: 3 },
};

/** Priority meta for one key; null = 未设置（不渲染标签）. */
export function priorityMeta(p) {
  return p ? (PRIORITY_META[p] || null) : null;
}

/** One task's priority sort rank: 未设置恒 9. */
export function priorityRank(t) {
  return (t && PRIORITY_META[t.priority]) ? PRIORITY_META[t.priority].rank : 9;
}

/** The priority chip's HTML ('' when unset) — kanban/list/drawer share it. */
export function priorityChipHTML(t) {
  const m = priorityMeta(t && t.priority);
  return m ? `<span class="pri ${m.cls}">${m.label}</span>` : '';
}

/** 待确认 rides above the machine: a task under proposal shows its own tag. */
export const PENDING_META = { label: t('待确认'), cls: 'st-pending', dot: '#ff8800' };

/** Status meta for one task (proposal-aware). */
export function statusMeta(t) {
  return t.pending ? PENDING_META : (STATUS_META[t.status] || { label: t.status || '—', cls: '', dot: '#8f959e' });
}

/** done/cancelled — the engine refuses further updates on these. */
export function isClosed(t) { return t.status === 'done' || t.status === 'cancelled'; }

/** The numeric progress: progress_pct first, the text note's percent as fallback display. */
export function pctOf(t) {
  if (t.progress_pct > 0) return t.progress_pct;
  const m = /(\d{1,3})\s*%/.exec(t.progress || '');
  return m ? Math.min(100, parseInt(m[1], 10)) : 0;
}

/** derived.go IsOverdue's display twin: past end while still open. */
export function isOverdue(t, now = Date.now() / 1000) {
  return t.end_ts > 0 && now > t.end_ts && (t.status === 'todo' || t.status === 'doing');
}

/** Has at least one child parented on id — the non-leaf test (progress derives). */
export function hasChildren(tasks, id) {
  return tasks.some((x) => x.parent === id);
}

/** Children of id, id order (structure.go keeps the tree per project). */
export function childrenOf(tasks, id) {
  return tasks.filter((x) => x.parent === id).sort((a, b) => a.id.localeCompare(b.id));
}

const pad = (n) => String(n).padStart(2, '0');

/** MM-DD (same year) or YYYY-MM-DD. */
export function fmtDay(ts) {
  const d = new Date(ts * 1000);
  const md = `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  return d.getFullYear() === new Date().getFullYear() ? md : `${d.getFullYear()}-${md}`;
}

/**
 * The due chip fact: null (no end), 逾期 (red, days past), 今天 (orange,
 * with time), else MM-DD 截止. The kanban/list/drawer all render this.
 */
export function dueChip(task, now = Date.now() / 1000) {
  if (!task.end_ts) return null;
  // 已结束的任务只留素色日期——逾期红/今天橙都是"还来得及"的信号
  if (task.status === 'done' || task.status === 'cancelled') return { text: t('{d} 截止', { d: fmtDay(task.end_ts) }), cls: 'dim' };
  if (isOverdue(task, now)) {
    const days = Math.max(1, Math.floor((now - task.end_ts) / 86400));
    return { text: t('已逾期 {n} 天', { n: days }), cls: 'overdue' };
  }
  const end = new Date(task.end_ts * 1000);
  const today = new Date();
  if (end.getFullYear() === today.getFullYear() && end.getMonth() === today.getMonth() && end.getDate() === today.getDate()) {
    return { text: t('今天 {hm} 截止', { hm: `${pad(end.getHours())}:${pad(end.getMinutes())}` }), cls: 'soon' };
  }
  return { text: t('{d} 截止', { d: fmtDay(task.end_ts) }), cls: '' };
}

/** datetime-local ↔ the wire's human encoding ("YYYY-MM-DD HH:MM"). */
export function tsToLocal(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function localToSched(value) {
  // "YYYY-MM-DDTHH:MM" → "YYYY-MM-DD HH:MM" (the wire's input encoding);
  // '' → '' (leave unchanged).
  const v = String(value || '').trim();
  return v ? v.replace('T', ' ') : '';
}
