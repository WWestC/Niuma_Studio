// 工作室脉搏 — 形态 B 示例插件（niuma.pulse）。
//
// 插件与宿主的全部契约就一个导出：activate(niuma)。niuma.* 按
// plugin.json 声明的 permissions 裁剪（这里声明了 boards / widgets /
// chat.read / projects.read）；没声明的域一调即抛错。这块插件演示
// 四件事：
//   ① registerBoard —— 挂一块自己的工作台板块（nav 锚点宿主管）；
//   ② registerWidget —— 往房间画板左上角挂一枚 HUD 计数徽章（不进
//      板块也在跳——计数活在 activate 层，不绑在板块的懒装载上）；
//   ③ chat.watch —— 只读订阅全部房间的活对话帧（say/report）；
//   ④ projects.list —— REST 读面拉项目清单。
// 回调里抛错不会伤到工作室（loader 兜住），但请还是别抛。

export function activate(niuma) {
  // 活数据住在 activate 层：板块懒装载（人不点 #/pulse 就不 mount）
  // 不该决定计数是否存在——HUD 徽章从第一帧起就该是活的
  const counts = new Map(); // roomKey → 对话行数
  const last = [];          // 最近 8 条 [{room, from, text}]
  let total = 0;
  let els = null;           // 板块视图的锚点（懒装载后才非空）
  let hudNum = null;        // HUD 徽章的数字位

  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));

  const paintBoard = () => {
    if (!els) return;
    const rooms = [...counts.entries()].sort((a, b) => b[1] - a[1]);
    els.counts.innerHTML = rooms.length
      ? rooms.map(([key, n]) => `<div class="pl-row"><span>${esc(key)}</span><b>${n}</b></div>`).join('')
      : '<div class="pl-empty">还没有动静——去对话板说句话试试</div>';
    els.last.innerHTML = last.length
      ? last.map((l) => `<div class="pl-line"><span class="pl-room">${esc(l.room)}</span><span class="pl-from">${esc(l.from || '?')}</span><span class="pl-text">${esc(l.text)}</span></div>`).join('')
      : '<div class="pl-empty">（等待第一条消息…）</div>';
  };

  niuma.chat.watch((f, key) => {
    if (f?.type !== niuma.wire.Say && f?.type !== niuma.wire.Report) return;
    counts.set(key, (counts.get(key) || 0) + 1);
    total += 1;
    last.unshift({ room: key, from: f.from, text: f.text });
    if (last.length > 8) last.pop();
    paintBoard();
    if (hudNum) hudNum.textContent = String(total);
  });

  // HUD 挂件：办公室画板左上角的一枚动静徽章（widgets 通道）
  niuma.registerWidget({
    spot: 'room-hud',
    mount(el) {
      el.title = '全部房间的对话动静（示例插件 工作室脉搏）';
      el.innerHTML = `<b>脉搏</b> <span style="color:var(--gold);font-weight:600">${total}</span>`;
      hudNum = el.querySelector('span');
    },
  });

  niuma.registerBoard({
    async mount(root) {
      root.innerHTML = `
        <style>
          .pl-wrap { padding: 24px; max-width: 720px; margin: 0 auto; font-size: 13px; }
          .pl-wrap h2 { font-size: 15px; margin: 0 0 4px; }
          .pl-sub { color: var(--chalk-dim, #8a9186); margin-bottom: 16px; }
          .pl-card { border: 1px solid var(--line, rgba(128,128,128,.25)); border-radius: 10px; padding: 12px 16px; margin-bottom: 16px; }
          .pl-row { display: flex; justify-content: space-between; padding: 3px 0; }
          .pl-line { display: flex; gap: 8px; padding: 3px 0; }
          .pl-room { color: var(--chalk-dim, #8a9186); flex: none; min-width: 72px; }
          .pl-from { flex: none; min-width: 64px; font-weight: 500; }
          .pl-text { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
          .pl-empty { color: var(--chalk-dim, #8a9186); }
        </style>
        <div class="pl-wrap">
          <h2>工作室脉搏</h2>
          <div class="pl-sub">示例插件 ${niuma.plugin.id} v${niuma.plugin.version} · 插件 API ${niuma.version} · 当前房：${niuma.rooms.current().name || niuma.rooms.current().key}</div>
          <div class="pl-card" id="pl-projects">项目清单读取中…</div>
          <div class="pl-card"><b>各房动静</b><div id="pl-counts"></div></div>
          <div class="pl-card"><b>最近 8 条</b><div id="pl-last"></div></div>
        </div>`;
      els = {
        projects: root.querySelector('#pl-projects'),
        counts: root.querySelector('#pl-counts'),
        last: root.querySelector('#pl-last'),
      };
      paintBoard();
      // REST 读面：项目清单（一次即可，演示 projects.read）
      try {
        const projects = await niuma.projects.list();
        const names = (projects || []).map((p) => p.name || p.key).filter(Boolean);
        els.projects.innerHTML = `<b>项目（${names.length}）</b><div>${names.length ? esc(names.join('、')) : '尚无项目'}</div>`;
      } catch (err) {
        els.projects.innerHTML = `<b>项目清单读取失败</b><div>${esc(err?.message || err)}</div>`;
      }
    },
  });
}
