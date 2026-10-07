// 插件市场 — 官方形态 B 插件（niuma.market，出厂预装）：吃自己狗粮的
// 第一个真插件——市场板块本身就是用插件 API 写的。一键安装走宿主的
// /market 面（market.admin 权限）：URL zip（带 sha256 钉）或本地目录；
// 启停/卸载即时生效（服务端当场重放贡献），装了带板块的插件刷新窗口
// 即见。源地址存 localStorage（本插件自己的键，不动宿主偏好）。

const OFFICIAL_SOURCE = 'https://raw.githubusercontent.com/WWestC/niuma-market/main/index.json';
const SOURCE_KEY = 'dh.plugin.niuma.market.source';

export function activate(niuma) {
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));

  let root = null;
  let sourceInput = null;
  let pathInput = null;
  let shelfEl = null;
  let noteEl = null;
  let installed = new Map(); // id → {enabled, manifest, problems}
  let entries = [];          // registry shelf
  let busy = false;

  const savedSource = () => {
    try { return localStorage.getItem(SOURCE_KEY) || OFFICIAL_SOURCE; } catch { return OFFICIAL_SOURCE; }
  };

  const toast = (text, kind) => niuma.ui.toast(text, kind);

  async function refreshInstalled() {
    try {
      const r = await fetch('/plugins.json');
      const idx = await r.json();
      installed = new Map((idx.plugins || []).map((p) => [p.id, p]));
    } catch {
      installed = new Map();
    }
  }

  function card(entryLike) {
    const inst = installed.get(entryLike.id);
    const state = inst
      ? (inst.enabled
        ? '<span class="mk-on">已启用</span>'
        : '<span class="mk-off">已停用</span>')
      : '';
    const actions = inst
      ? `<button type="button" class="mk-btn" data-act="toggle" data-id="${esc(entryLike.id)}">${inst.enabled ? '停用' : '启用'}</button>` +
        `<button type="button" class="mk-btn mk-danger" data-act="remove" data-id="${esc(entryLike.id)}">卸载</button>`
      : `<button type="button" class="mk-btn mk-primary" data-act="install" data-id="${esc(entryLike.id)}" data-url="${esc(entryLike.url || '')}" data-sha="${esc(entryLike.sha256 || '')}">一键安装</button>`;
    const probs = inst?.problems?.length
      ? `<div class="mk-probs">⚠ ${inst.problems.map(esc).join('；')}</div>` : '';
    return `<div class="mk-card" data-card="${esc(entryLike.id)}">
      <div class="mk-head"><b>${esc(entryLike.name || entryLike.id)}</b>${state}</div>
      <div class="mk-meta">${esc(entryLike.id)} · v${esc(entryLike.version || '?')}${entryLike.author ? ' · ' + esc(entryLike.author) : ''}</div>
      <div class="mk-desc">${esc(entryLike.desc || '')}</div>
      ${probs}
      <div class="mk-actions">${actions}</div>
    </div>`;
  }

  function paint() {
    if (!shelfEl) return;
    // 货架 = 源条目 ∪ 已安装（本地导入的不在源上也可见、可管理）
    const seen = new Set();
    const rows = [];
    for (const e of entries) {
      seen.add(e.id);
      rows.push(card(e));
    }
    for (const [id, inst] of installed) {
      if (seen.has(id)) continue;
      const m = inst.manifest || {};
      rows.push(card({
        id,
        name: m.name || id,
        version: m.version,
        author: m.author,
        desc: (m.desc || '') + '（本地安装，不在当前源上）',
      }));
    }
    shelfEl.innerHTML = rows.length
      ? rows.join('')
      : '<div class="mk-empty">货架是空的——填个源地址「载入货架」，或用下面的本地导入。</div>';
  }

  async function loadShelf() {
    const url = (sourceInput.value || '').trim();
    if (!url) { toast('先填源地址', 'err'); return; }
    try { localStorage.setItem(SOURCE_KEY, url); } catch { /* 无 localStorage 环境照常工作 */ }
    noteEl.textContent = '正在取货架…';
    try {
      const idx = await niuma.market.index(url);
      entries = idx.plugins || [];
      noteEl.textContent = `源就绪：${entries.length} 个插件`;
    } catch (err) {
      entries = [];
      noteEl.textContent = `源不可达：${err?.message || err}（官方源要在 GitHub 建 niuma-market 仓库后开张；本地玩先用下面的本地导入）`;
    }
    paint();
  }

  async function onAction(btn) {
    if (busy) return;
    const act = btn.dataset.act;
    const id = btn.dataset.id;
    busy = true;
    btn.disabled = true;
    try {
      if (act === 'install') {
        const body = btn.dataset.url
          ? { url: btn.dataset.url, sha256: btn.dataset.sha || '' }
          : { path: btn.dataset.path || '' };
        const res = await niuma.market.install(body);
        toast(res.text || `已安装 ${id}`);
        await refreshInstalled();
        paint();
        // 装的插件带板块 → 刷新窗口长出来（市场自己不怕重载，预装件）
        const inst = installed.get(id);
        if (inst?.manifest?.contributes?.boards?.length) {
          setTimeout(() => location.reload(), 900);
        }
      } else if (act === 'toggle') {
        const on = !installed.get(id)?.enabled;
        const res = await niuma.market.setEnabled(id, on);
        toast(res.text || '已更新');
        await refreshInstalled();
        paint();
      } else if (act === 'remove') {
        const res = await niuma.market.remove(id);
        toast(res.text || `已卸载 ${id}`);
        await refreshInstalled();
        paint();
      }
    } catch (err) {
      toast(`${act} 失败：${err?.message || err}`, 'err');
      btn.disabled = false;
    } finally {
      busy = false;
    }
  }

  niuma.registerBoard({
    async mount(el) {
      root = el;
      root.innerHTML = `
        <style>
          .mk-wrap { padding: 24px; max-width: 860px; margin: 0 auto; font-size: 13px; }
          .mk-wrap h2 { font-size: 15px; margin: 0 0 4px; }
          .mk-sub { color: var(--chalk-dim, #8a9186); margin-bottom: 16px; }
          .mk-src { display: flex; gap: 8px; margin-bottom: 8px; }
          .mk-src input, .mk-path input { flex: 1; }
          .mk-input { border: 1px solid var(--line-2, #d0d3d6); border-radius: 8px; padding: 7px 10px; font: inherit; background: var(--board, #fff); color: var(--chalk, #1f2329); min-width: 0; }
          .mk-note { color: var(--chalk-faint, #8f959e); margin-bottom: 16px; min-height: 1em; }
          .mk-shelf { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 12px; }
          .mk-card { border: 1px solid var(--line, #dee0e3); border-radius: 10px; padding: 12px 14px; display: flex; flex-direction: column; gap: 4px; background: var(--board, #fff); }
          .mk-head { display: flex; justify-content: space-between; align-items: center; }
          .mk-on { color: var(--ok, #34c724); font-size: 12px; }
          .mk-off { color: var(--chalk-faint, #8f959e); font-size: 12px; }
          .mk-meta { color: var(--chalk-faint, #8f959e); font-size: 12px; }
          .mk-desc { color: var(--chalk-dim, #646a73); min-height: 2.6em; }
          .mk-probs { color: var(--warn, #ff8800); font-size: 12px; }
          .mk-actions { margin-top: auto; display: flex; gap: 8px; justify-content: flex-end; }
          .mk-btn { border: 1px solid var(--line-2, #d0d3d6); border-radius: 7px; padding: 4px 12px; font: inherit; cursor: pointer; background: var(--board, #fff); color: var(--chalk, #1f2329); }
          .mk-btn.mk-primary { background: var(--gold, #1456f0); border-color: var(--gold, #1456f0); color: #fff; }
          .mk-btn.mk-danger { color: var(--bad, #f54a45); }
          .mk-btn:disabled { opacity: .5; cursor: default; }
          .mk-empty { color: var(--chalk-faint, #8f959e); grid-column: 1 / -1; }
          .mk-path { display: flex; gap: 8px; margin-bottom: 16px; }
        </style>
        <div class="mk-wrap">
          <h2>插件市场</h2>
          <div class="mk-sub">一键装插件——源货架或本地目录；启停/卸载即时生效（新板块刷新窗口即见）</div>
          <div class="mk-src">
            <input class="mk-input" type="url" placeholder="源地址（index.json）" value="${esc(savedSource())}">
            <button type="button" class="mk-btn mk-primary" data-mk-load>载入货架</button>
          </div>
          <div class="mk-path">
            <input class="mk-input" type="text" placeholder="本地导入：插件目录路径（含 plugin.json）">
            <button type="button" class="mk-btn" data-mk-import>导入</button>
          </div>
          <div class="mk-note" data-mk-note></div>
          <div class="mk-shelf" data-mk-shelf></div>
        </div>`;
      sourceInput = root.querySelector('.mk-src input');
      pathInput = root.querySelector('.mk-path input');
      noteEl = root.querySelector('[data-mk-note]');
      shelfEl = root.querySelector('[data-mk-shelf]');
      root.querySelector('[data-mk-load]').addEventListener('click', () => loadShelf());
      root.querySelector('[data-mk-import]').addEventListener('click', async () => {
        const path = (pathInput.value || '').trim();
        if (!path) { toast('先填插件目录路径', 'err'); return; }
        try {
          const res = await niuma.market.install({ path });
          toast(res.text || '已导入');
          await refreshInstalled();
          paint();
          const id0 = res.id;
          if (installed.get(id0)?.manifest?.contributes?.boards?.length) {
            setTimeout(() => location.reload(), 900);
          }
        } catch (err) {
          toast(`导入失败：${err?.message || err}`, 'err');
        }
      });
      shelfEl.addEventListener('click', (ev) => {
        const btn = ev.target.closest('[data-act]');
        if (btn) onAction(btn);
      });
      await refreshInstalled();
      paint();
      await loadShelf();
    },
  });
}
