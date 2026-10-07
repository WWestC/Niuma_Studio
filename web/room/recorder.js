// recorder.js — the 一日回放 recording leg (r_32): the owner page's
// frames-to-disk pump. 第一性：小人位置只活在持有模拟的前端进程里，
// 后端无名册之外的坐标真值——「保存住一举一动」的唯一路径就是持有模
// 拟的这一页把快照流送下去。设计踩 r_23 的便车：采样节拍（0.5s）由
// ReplayBuf.sample 出帧，本模块只做「变化过滤 + 攒批 + 定时冲账」：
//
//   变化过滤 — sameSample（replay.js）：空闲时段一帧都不多发，活跃
//              一天 10–20MB 量级；
//   攒批     — 20s 一批 POST /p/{key}/replay（服务端水位线让重发幂
//              等，丢批不重试、下一批全量补上）；
//   冲账点   — 定时器 / 切房（旧 key 先冲）/ pagehide·hidden（fetch
//              keepalive 尽力送达最后一截）。
//
// 只有房主页挂（!__NIUMA_VISITOR__）——访客页不录，一办公室一个录
// 制者，房主的模拟才是「正史」。

import { sameSample } from './replay.js';

export const FLUSH_EVERY = 20_000;  // ms — 定时冲账节拍
export const FEED_DEBOUNCE = 250;   // ms — 同拍重采去抖（sample 的栅格对齐在 replayBuf 侧）

// Recorder batches sampled frames to the server. post 是注入的
// (key, frames, opts) → Promise（api.postReplay 的形状——测试注入假
// 桩，不碰网络）。
export class Recorder {
  constructor(post, opts = {}) {
    this.post = post;
    this.key = '';
    this.pending = [];     // [{t: 墙钟 ms, a: [nine-field actors]}]
    this.lastKept = null;  // 上一张落盘帧（变化过滤的对照面）
    this.lastFedAt = 0;
    this.timer = null;
    this.onFlush = opts.onFlush || null; // (sent: number) — 测试/遥测钩子
    this._unload = null;
  }

  // start boots the interval + the lifecycle taps. 幂等（双调无副作用）。
  // addEventListener/document 有无守卫——node --test 的裸环境也能构造。
  start() {
    if (this.timer) return;
    this.timer = setInterval(() => this.flush(), FLUSH_EVERY);
    this._unload = (e) => {
      if (e && e.type === 'visibilitychange' &&
          typeof document !== 'undefined' && document.visibilityState !== 'hidden') {
        return; // 只冲 hidden——visible 的那拍没东西急着送
      }
      this.flush({ keepalive: true });
    };
    if (typeof addEventListener === 'function') {
      addEventListener('pagehide', this._unload);
    }
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', this._unload);
    }
  }

  stop() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    if (this._unload) {
      if (typeof removeEventListener === 'function') {
        removeEventListener('pagehide', this._unload);
      }
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', this._unload);
      }
      this._unload = null;
    }
    this.flush();
  }

  // attach names the room being recorded; a key change flushes the old
  // room's tail first (帧不能落错房).
  attach(key) {
    if (key === this.key) return;
    if (this.pending.length) this.flush();
    this.key = key;
    this.lastKept = null; // 新房无对照面：第一帧必发
    this.lastFedAt = 0;   // 去抖钟也归零——新房的节拍从新起
  }

  // feed is sample()'s twin call site: one frame per 0.5s tick. snap 是
  // sample 的产出 {a, r}（r_32.2 房间级视态随帧同行；裸数组——纯演员
  // 面的老用法——也收）。
  feed(tMs, snap) {
    if (!this.key || !snap) return false;
    const list = Array.isArray(snap) ? snap : snap.a;
    if (!list || !list.length) return false;
    if (tMs - this.lastFedAt < FEED_DEBOUNCE) return false; // 同拍重采
    if (this.lastKept && sameSample(this.lastKept, snap)) return false; // 无变化不上账
    this.lastFedAt = tMs;
    this.lastKept = snap;
    const room = Array.isArray(snap) ? undefined : snap.r;
    this.pending.push(room === undefined ? { t: tMs, a: list } : { t: tMs, a: list, r: room });
    return true;
  }

  // flush posts the batch and clears it (失败也清——服务端水位线让
  // 下一批的更晚帧顶上，丢的是 20s 尾巴，不是一致性).
  flush(opts = {}) {
    const n = this.pending.length;
    if (!n || !this.key) return 0;
    const frames = this.pending;
    this.pending = [];
    const p = this.post(this.key, frames, opts);
    if (p && typeof p.catch === 'function') p.catch(() => {});
    if (this.onFlush) this.onFlush(n);
    return n;
  }
}
