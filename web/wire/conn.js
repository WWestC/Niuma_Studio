// conn.js — the workbench's two connection shapes (frontend 稿 §4.6):
//
//   ObserverConn  one per room, opened at boot and never torn down by
//                 a mere view switch — 切房=视图切换不断流. Seatless,
//                 read-only, receives every broadcast. Owns the §4.6-2
//                 resilience kit: capped exponential backoff, the
//                 60s inbound watchdog (§4.6-3 — fed by the server's
//                 per-observer app-level ping frames, because the
//                 browser hides ping/pong control frames from JS: a
//                 quiet room stays connected, only a genuinely dead
//                 transport trips the watchdog; each forced reconnect
//                 resyncs the roster off the welcome frame and
//                 backfills the seq gap off /p/{key}/history, which
//                 keeps the cycle cheap AND self-healing), and the
//                 seq-cursor gap backfill (say/report are monotonic —
//                 the jsonl returns everything past the cursor,
//                 oldest first).
//                 Initial hydration rides the SAME backfill slot but
//                 reads the log's TAIL — the newest MESSAGE_CAP frames
//                 (v2.8.1: head-first paging hid the newest conversation
//                 of every room past one batch). Either way backfilled
//                 frames arrive marked non-live, so a page load counts
//                 ZERO unread — history is already read, only frames
//                 that arrive live while the room sits unfocused badge.
//
//   OwnerChannel  the write face, one per room, opened lazily on the
//                 first send (§4.6-1's 观察者读＋房主写 split). The
//                 owner-named hello rides the seatless owner face in
//                 EVERY room while the owner's seat there is live (the
//                 registry pre-seats the owner into each project room,
//                 v2 P7 lobby parity: no seat, receipt per frame); a
//                 room without a live owner seat lands the same dial
//                 as a plain owner-named seat. Delivery is acked by the
//                 say echo this face always returns; anything still in
//                 flight when the transport dies is reported, never
//                 swallowed.
//
// Neither class renders anything — rooms.js owns state, the views own
// DOM. Frames and strings are the only things crossing this boundary.

import { ProtoVersion, Msg, LobbyKey, MESSAGE_CAP } from './wire.js';
import { getHistory } from './api.js';
import { t } from '../ui/i18n.js';

const HEARTBEAT_WATCHDOG_MS = 60_000; // §4.6-3: no inbound frame → force reconnect. The server's per-observer app-level pings (every Heartbeat tick, 25s in production) feed it — a quiet room no longer trips it, only a dead transport does.
const BACKOFF_START_MS = 500;         // §4.6-2: 0.5s → 1 → 2 → 4 → 8 … 30s cap
const BACKOFF_CAP_MS = 30_000;

function wsURL() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${location.host}/ws`;
}

export class ObserverConn {
  /**
   * @param {string} project room key ("default" = the lobby)
   * @param {Object} ev
   * @param {(f: import('./wire.js').Frame) => void} ev.onFrame live frames, in delivery order
   * @param {(list: import('./wire.js').Frame[], initial: boolean) => void} ev.onBackfill gap frames replayed after a reconnect or, with initial=true, the room's first hydration (roster is welcome-authoritative — these are message lines only)
   * @param {(mode: string, tries: number) => void} ev.onState connecting|online|reconnecting|offline — tries is the reconnect's 1-based backoff ordinal (0 elsewhere), the §4.1④ 重连中(第 N 次退避) label
   * @param {(text: string) => void} [ev.onNotice] transport-level notes (backfill failure)
   * @param {string} [visitor] r_19 访客 token（/view 分享页才带）——带上它
   *   服务端才把本拨号计为访客（限流/TTL）；房主自己的窗口永远不带
   */
  constructor(project, ev, visitor = '') {
    this.project = project;
    this.ev = ev;
    this.visitor = visitor;
    this.sock = null;
    this.stopped = false;
    this.attempts = 0;
    this.lastSeq = 0;      // every seen frame's max seq (the backfill cursor)
    this.firstDial = true; // the initial hydration backfill's label flag
    this.gating = false;   // a backfill is in flight: hold live frames to keep order
    this.pending = [];     // …the frames held while gating
  }

  start() { this.#dial(); }

  stop() {
    this.stopped = true;
    clearTimeout(this.watchdog);
    clearTimeout(this.retry);
    if (this.sock) { const s = this.sock; this.sock = null; s.close(); }
  }

  #setState(mode, tries = 0) { this.ev.onState(mode, tries); }

  #armWatchdog() {
    clearTimeout(this.watchdog);
    this.watchdog = setTimeout(() => { if (this.sock) this.sock.close(); }, HEARTBEAT_WATCHDOG_MS);
  }

  #dial() {
    this.#setState(this.firstDial ? 'connecting' : 'reconnecting', this.attempts);
    this.#armWatchdog();
    const sock = new WebSocket(wsURL());
    this.sock = sock;
    sock.onopen = () => {
      // replay stays false on every dial: history comes in through the
      // post-welcome backfill below, badge-free by construction.
      // 访客页带 token：服务端只认带 token 的拨号为访客（治理面），
      // 房主窗口不带——永不进访客计数。
      const hello = { type: Msg.Hello, observer: true, project: this.project, replay: false, proto: ProtoVersion };
      if (this.visitor) hello.visitor = this.visitor;
      sock.send(JSON.stringify(hello));
    };
    sock.onmessage = (e) => {
      this.#armWatchdog();
      let frame;
      try { frame = JSON.parse(e.data); } catch { return; }
      if (frame.type === Msg.Ping) return; // server keepalive: watchdog fed, nothing downstream sees it
      if (this.gating) { this.pending.push(frame); return; } // backfill in flight: hold order
      this.#deliver(frame);
    };
    sock.onclose = () => {
      if (this.sock !== sock || this.stopped) return;
      this.sock = null;
      this.firstDial = false;
      this.attempts += 1;
      this.#setState('reconnecting', this.attempts);
      const wait = Math.min(BACKOFF_START_MS * 2 ** (this.attempts - 1), BACKOFF_CAP_MS);
      this.retry = setTimeout(() => this.#dial(), wait);
    };
    sock.onerror = () => sock.close();
  }

  #deliver(frame) {
    if (frame.seq > this.lastSeq) this.lastSeq = frame.seq;
    if (frame.type === Msg.Welcome) {
      this.attempts = 0;
      this.#setState('online');
      this.#backfill();
    }
    // 死链落轨（r_19）：error 帧 404 = 访客 token 已废（通道关了/纪元
    // 换了）——重连永远只会再收 404，停摆并落 offline（HTTP 读面若仍
    // 活，界面自然落「仅缓存」，如实）。503/429 的拥挤拒绝不在此列：
    // 那些等得起，退避照旧。
    if (frame.type === 'error' && frame.status === 404) {
      this.stopped = true;
      clearTimeout(this.retry);
      clearTimeout(this.watchdog);
      this.#setState('offline');
    }
    this.ev.onFrame(frame);
  }

  // §4.6-2 gap backfill. Two shapes share this slot:
  //
  //   initial hydration (first dial, lastSeq 0) reads the TAIL — the
  //   newest MESSAGE_CAP frames — not the file's first page. Oldest-
  //   first paging alone pinned a cold open to the head of the log, so
  //   a room past one batch reopened with its newest conversation
  //   invisible, and the first live frame then jumped lastSeq past the
  //   hidden gap — unrecoverable until the next cold open (v2.8.1).
  //
  //   reconnect backfill (lastSeq > 0) pages FORWARD from the cursor,
  //   chasing next_since until has_more clears — the wire contract,
  //   now actually honored: a gap past one batch completes too.
  //
  // Both end with lastSeq owning everything emitted, so the gated-live
  // drop below stays sound either way.
  async #backfill() {
    this.gating = true;
    this.pending = this.pending || [];
    const initial = this.firstDial;
    try {
      let frames;
      if (initial) {
        const page = await getHistory(this.project, 0, { tail: true, limit: MESSAGE_CAP });
        frames = page.messages || [];
        for (const f of frames) {
          if (f.seq > this.lastSeq) this.lastSeq = f.seq;
        }
      } else {
        frames = [];
        let cursor = this.lastSeq;
        for (;;) {
          const page = await getHistory(this.project, cursor);
          const batch = page.messages || [];
          frames.push(...batch);
          for (const f of batch) {
            if (f.seq > this.lastSeq) this.lastSeq = f.seq;
          }
          // the documented chase: keep paging until the gap is drained.
          // An empty batch or a stuck cursor is the loop's hard stop.
          if (!page.has_more || !page.next_since || !batch.length || page.next_since <= cursor) break;
          cursor = page.next_since;
        }
      }
      if (!this.stopped) this.ev.onBackfill(frames, initial);
    } catch (err) {
      this.ev.onNotice?.(t('历史补窗失败（{room}）：{msg}', { room: this.project, msg: err.message }));
    } finally {
      this.gating = false;
      // a frame recorded while the scan was streaming may arrive BOTH
      // ways (jsonl line and live broadcast) — the cursor now covers
      // it, so drop the gated copy
      const held = this.pending.filter((f) => !f.seq || f.seq > this.lastSeq);
      this.pending = [];
      if (!this.stopped) for (const f of held) this.#deliver(f);
    }
  }
}

export class OwnerChannel {
  /**
   * @param {string} key room key; the lobby dial omits hello.project so
   *   the server's mirror/seated split resolves itself
   * @param {{name: string, role?: string}} owner the local:true person
   * @param {Object} ev
   * @param {(text: string) => void} ev.onNotice write-face receipts the UI must show in place (failures, seat downgrades)
   * @param {(f: import('./wire.js').Frame) => void} [ev.onFrame] every inbound frame of this face — management receipts (kick/rank_set system replies, capability saved/assembled/denied) ride here; the face itself renders nothing
   */
  constructor(key, owner, ev) {
    this.key = key;
    this.owner = owner;
    this.ev = ev;
    this.sock = null;
    this.open = false;
    this.queue = [];    // {frame, label} waiting for the transport
    this.inflight = []; // sent says awaiting the echo (delivery ack)
  }

  /** Queue one say; dials on first use. Delivery renders via the room's observer.
   * images（输入图片）是已上传的媒体引用（POST /media 的回执）——字节不
   * 进 WS，历史与广播只带引用。quote（结构化引用回复）以字段随帧——
   * 正文保持干净，复制/转发不再把引用头带下来，引用即点名由服务端按
   * 字段解析。 */
  say(text, images, quote) {
    if (!text && !(images && images.length)) return;
    const frame = { type: Msg.Say, text };
    if (images && images.length) frame.images = images;
    if (quote) frame.quote = quote;
    this.send(frame, images && images.length ? t('图片×{n}', { n: images.length }) : text);
  }

  /**
   * Queue one management frame (kick / rank_set / agent_archive /
   * assemble / skill_save / mcp_save — the lobby seatless face speaks them
   * all); dials on first use. Receipts land on ev.onFrame, never here.
   * @param {import('./wire.js').Frame} frame
   * @param {string} [label] human hint for the lost-in-flight notice
   */
  send(frame, label = frame.type) {
    this.stopped = false; // a send after bye re-arms the channel
    this.queue.push({ frame, label });
    if (this.sock) this.#flush();
    else this.#dial();
  }

  /** Polite teardown (pagehide): bye releases a project-room seat at once, no grace ghost. */
  bye() {
    this.stopped = true; // an in-flight credential fetch must not resurrect the transport
    if (this.sock && this.open) {
      try { this.sock.send(JSON.stringify({ type: Msg.Bye })); } catch { /* already dead */ }
    }
    this.#teardown();
  }

  #dial() {
    // owner-M1: the hello carries the owner seat credential — speech on
    // this face is proof-checked frame by frame server-side, the name
    // alone stopped being an identity (the mirror impersonation
    // incident). One fetch per dial (dials are rare: first send per
    // room, reconnects); a 404/miss dials bare and the server's
    // refusal names the fix.
    fetch('/owner/token')
      .then((r) => (r.ok ? r.json() : null))
      .then((j) => this.#dialWith((j && j.token) || ''))
      .catch(() => this.#dialWith(''));
  }

  #dialWith(token) {
    if (this.sock || this.stopped) return; // a newer dial owns the transport, or bye won
    const hello = {
      type: Msg.Hello, name: this.owner.name,
      role: this.owner.role || t('房主'), replay: false, proto: ProtoVersion,
    };
    if (token) hello.token = token;
    if (this.key !== LobbyKey) hello.project = this.key;
    const sock = new WebSocket(wsURL());
    this.sock = sock;
    sock.onopen = () => sock.send(JSON.stringify(hello));
    sock.onmessage = (e) => {
      let frame;
      try { frame = JSON.parse(e.data); } catch { return; }
      this.#intake(frame);
    };
    sock.onclose = () => {
      if (this.sock !== sock) return;
      this.#teardown();
      const lost = [...this.queue, ...this.inflight.map((t) => ({ label: t }))];
      this.queue = [];
      this.inflight = [];
      if (lost.length) this.ev.onNotice(t('发送连接中断，{n} 条未确认送达：{list}', { n: lost.length, list: lost.map((i) => i.label).join(' / ') }));
    };
    sock.onerror = () => sock.close();
  }

  #teardown() {
    if (this.sock) { const s = this.sock; this.sock = null; s.close(); }
    this.open = false;
  }

  #flush() {
    while (this.open && this.queue.length) {
      const item = this.queue.shift();
      if (item.frame.type === Msg.Say) this.inflight.push(item.label);
      try {
        this.sock.send(JSON.stringify(item.frame));
      } catch {
        this.queue.unshift(item);
        if (item.frame.type === Msg.Say) this.inflight.pop();
        break;
      }
    }
  }

  // The write face renders nothing: say echoes ack delivery (the
  // room's observer shows the line), system frames carry refusals and
  // receipts, capability/task/plan frames carry operation outcomes —
  // the last three go to ev.onFrame for the boards to judge.
  #intake(frame) {
    switch (frame.type) {
      case Msg.Welcome:
        this.open = true;
        if (frame.you && frame.you.name !== this.owner.name) {
          this.ev.onNotice(t('座位「{a}」被占用，本窗口将以「{b}」身份发言', { a: this.owner.name, b: frame.you.name }));
        }
        this.#flush();
        break;
      case Msg.Say: {
        // say 回显对账：文本 say 的 label 即全文；图片 say（可能无文
        // 本）的 label 是「图片×N」——与 #flush 压栈的同一枚 label。
        const label = frame.text || (frame.images && frame.images.length ? t('图片×{n}', { n: frame.images.length }) : '');
        const i = label ? this.inflight.indexOf(label) : -1;
        if (i >= 0 && frame.from === this.owner.name) this.inflight.splice(i, 1);
        break;
      }
      case Msg.System:
        this.ev.onNotice(frame.text || '');
        this.ev.onFrame?.(frame);
        break;
      case Msg.SkillEvt:
      case Msg.McpEvt:
      case Msg.Task:   // denied task frames ride this face privately
      case Msg.Plan:   // plan outcomes / cross-room receipts
      case Msg.Kb:     // doc write receipts (conflicts carry current_rev)
      case Msg.MemberWork: // the dispatcher's turn tell (roster chip + stream hint)
        this.ev.onFrame?.(frame);
        break;
      default:
        break;
    }
  }
}
