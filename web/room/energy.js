// energy.js — the 体力×零食×垃圾 engine (r_07, t_137/t_138): one
// hidden stamina scalar per member drives the office's life loop —
// 干活掉体力快、空闲也慢漏（没活干也要吃饭）→ 低体力觅食 → 吃零食
// 回复 → 残骸丢桶（满则落地软阻挡）→ 保洁阿姨清走。纯氛围模拟：体力
// 从不影响任务产出（那是工作系统的领地），一切数值不进服务端协议、
// 不落盘（重开页面重算——与摸鱼同一零后果纪律）。
//
// 调参区（定稿 §六）：EPARAMS 集中全部旋钮，改值即调参（ARTV 同款）。

import { sfx } from './sfxport.js';
import { t } from '../ui/i18n.js';
import { DIR } from './actor.js';
import { claimed, dispatchable, sendOnErrand, leaveDesk } from './errand.js';

// 吃链走位死线（秒）：被堵/目标不可达时散场零损失（库存到手才扣）
const EAT_WALK_DEADLINE = 15;

// ── 参数区（初值＝定稿 §一/§三/§六）─────────────────────────────
export const EPARAMS = {
  // 体力模型（§一）
  energyMax: 100,        // 上限
  drain: 0.35 / 60,      // 干活消耗/秒（0.35/min；会议不掉——开会养神）
  idleDrain: 0.25 / 60,   // 空闲消耗/秒（0.25/min——没活也要吃饭：慢漏保生活循环不断档）
  breakRegen: 8,         // 咖啡歇一次性回复（进歇脚位结算）
  eatGain: 30,           // 每份零食
  lowAt: 30,             // 低体力阈值：触发「想吃」
  critAt: 12,            // 饿过昏线：sleepy 挂头＋消耗减半
  hungryChance: 0.4,     // 低体力时每拍发起觅食概率（<critAt 强制 100%）
  // 零食架（§二）
  snackCapacity: 8,      // 架容量
  snackRestockAt: 2,     // ≤此值保洁顺手补（t_139 阿姨侧消费）
  // 垃圾桶（§三）
  binCapacity: 6,        // 桶容量
  binTossRange: 16,      // 丢垃圾站定距离（native）
  // 垃圾落地（§三）
  litterDecay: 90,       // 落地自然消失（秒）
  litterMax: 12,         // 全室垃圾帽（超过不再掉新垃圾）
  penalty: 6,            // 软阻挡代价（一步垃圾格等效 7 步）
  // 戏剧性例外（§五；默认全关，房主可单独开）
  drama: {
    spill: false,        // 失手落地 5%
    hoard: false,        // 囤货特调批评
    critEat: false,      // 饿过昏线硬吃加速
    fullBinToss: false,  // 对满桶硬丢抛物线
  },
};

// ── 体力引擎 ──────────────────────────────────────────────────────
export class EnergyEngine {
  /** @param {import('./roomview.js').RoomView} board */
  constructor(board) {
    this.board = board;
    this.energy = new Map();   // name → 0..100（缺省 35–90 错峰起——见 en()）
    this.breakPaid = new Map();// name → 本轮歇脚是否已结算（防重复回）
    this.snacks = EPARAMS.snackCapacity; // 零食架库存（架只有一座）
    this.bin = 0;              // 垃圾桶计数
    this.litter = [];          // 落地垃圾 [{x, y, born, crumb}]
    this.beatT = 0;
  }

  en(name) {
    if (!this.energy.has(name)) {
      // 冷启动错峰：不都从满值起（全员 100＝首顿要等几小时，开页半天
      // 看不到一次干饭）——随机 35–90 落位，最饿的半小时内先开饭
      this.energy.set(name, 35 + Math.random() * 55);
    }
    return this.energy.get(name);
  }

  // 每帧：消耗/恢复结算（秒级积分；critAt 下消耗减半——身体自保不罢工）
  step(dt) {
    const board = this.board;
    const busy = board.busy;
    for (const a of board.actors.values()) {
      if (a.isLocal || a.isNPC) continue; // 房主不需要吃饭；NPC 没有体力（t_139）
      let e = this.en(a.name);
      if (a.working && busy && busy.has(a.name)) {
        const rate = e < EPARAMS.critAt ? EPARAMS.drain / 2 : EPARAMS.drain;
        e = Math.max(0, e - rate * dt);
      } else if (a.breakIdx >= 0) {
        // 咖啡歇：进歇脚位一次性结算（同一轮歇脚只结一次）
        if (!this.breakPaid.get(a.name)) {
          this.breakPaid.set(a.name, true);
          e = Math.min(EPARAMS.energyMax, e + EPARAMS.breakRegen);
        }
      } else {
        // 空闲也饿（没活干也要吃东西）：慢漏而非回满，生活循环不因无活
        // 停摆；昏线下同样减半（身体自保与干活同律）
        const rate = e < EPARAMS.critAt ? EPARAMS.idleDrain / 2 : EPARAMS.idleDrain;
        e = Math.max(0, e - rate * dt);
        if (a.breakIdx < 0) this.breakPaid.delete(a.name);
      }
      // 饿过昏线：sleepy 挂头（低频冒泡，不刷屏）
      if (e < EPARAMS.critAt && Math.random() < dt * 0.1) {
        board.emoter.emote(a.name, 'sleepy');
      }
      this.energy.set(a.name, e);
    }
    // 垃圾自然衰减
    const now = board.clock || 0;
    for (let i = this.litter.length - 1; i >= 0; i--) {
      if (now - this.litter[i].born >= EPARAMS.litterDecay) this.litter.splice(i, 1);
    }
    // 觅食节拍（8s 一拍，与社交引擎同拍不同池）
    this.beatT -= dt;
    if (this.beatT <= 0) { this.beatT = 8; this.forageBeat(); }
  }

  // 觅食判定：低体力＋闲 → 走零食架吃一份。资格走 errand.js 一口径
  //（dispatchable：非本机/非忙/非会/非歇/非摸鱼团/无任何外勤在手/
  // 手里没件）；进食中断复用 endBreak 语义（stepEat 首行——只认外部
  // 占用，自己手里的 'food' 道具不再是「别人的取材」，根治一帧自弃）。
  forageBeat() {
    const board = this.board;
    const busy = board.busy;
    if (!busy) return; // 名册未就绪不派人（口径见 errand.js）
    for (const a of board.actors.values()) {
      if (a.isLocal || a.isNPC) continue;
      if (a.eating) continue; // 已在吃链中（显式早退，其余口径全在 dispatchable）
      if (!dispatchable(a, busy)) continue;
      const e = this.en(a.name);
      if (e >= EPARAMS.lowAt) continue;
      // <critAt 强制觅食；低体力 40% 概率
      const chance = e < EPARAMS.critAt ? 1 : EPARAMS.hungryChance;
      if (Math.random() > chance) continue;
      this.startEat(a);
      break; // 一拍只派一人（错峰吃饭，办公室不至于集体干饭）
    }
  }

  // 取食-吃-丢 行为链（与打印仪式同中断语义：派活/叫会立即散）。
  // 出发不预扣库存：零食到手那一刻才减——架上事实是真源，走到发现
  // 空架才是真售罄（白走一趟的失望本身就是戏）。
  startEat(a) {
    const board = this.board;
    const g = board.art.geom;
    if (!g || !g.pantry) return;
    // 走零食架（茶水间东臂 open shelving 的台面前）→ 吃 → 丢
    const px = g.pantry.rect[0] + g.pantry.rect[2] - 30;
    const py = g.pantry.rect[1] + g.pantry.rect[3] + 14;
    leaveDesk(a, board); // 暂离工位（回座缓冲保留，散场 idle 落座回原位）
    sendOnErrand(a, px, py, DIR.UP); // 面北朝货架；working 锚同摘（坐着派目标不动是旧疤）
    a.eating = { phase: 'walk', t: 0, born: board.clock || 0 };
    board.emoter.emote(a.name, 'happy');
  }

  // 吃链推进（挂在 board.update 之后：actor 到位后相位推进）
  stepEat(a, dt) {
    const board = this.board;
    const st = a.eating;
    if (!st) return;
    // 中断纪律：只认外部占用（errand.js 口径，own='eating'——自己手里
    // 的 'food' 道具不算数）；散场摘锚（working），路权归接管者。
    if (claimed(a, board.busy, 'eating')) {
      a.eating = null;
      a.holding = null;
      a.foodKey = '';
      a.working = false;
      return;
    }
    st.t += dt;
    switch (st.phase) {
      case 'walk':
        if ((board.clock || 0) - st.born > EAT_WALK_DEADLINE) {
          this.finishEat(a); // 死线散场：库存未扣，零损失
          return;
        }
        if (!a.hasTarget && !a.moving) { // 到位：开吃（或扑空）
          if (this.snacks > 0) {
            this.snacks--; // 到手才扣（真源对齐架上事实——旧版出发即扣，
            // 中断无退款，架上被「幻影吃空」是根因之一）
            // 挑食品（t_136 条目真形）：32 条目录确定性挑——同人同零食
            //（名字哈希），吃出「你的常吃零食」人设感
            const FOODS = globalThis.__NIUMA_FOODS__ || [];
            if (FOODS.length) {
              let h = 0;
              for (const ch of a.name) h = (h * 31 + ch.codePointAt(0)) >>> 0;
              a.foodKey = FOODS[h % FOODS.length].key;
            }
            st.phase = 'eat';
            st.t = 0;
            a.holding = 'food'; // 手持件（t_136 资产就位前 kind=food 占位渲染）
            board.emoter.emote(a.name, 'happy');
            sfx('scene-crunch'); // r_09：吃首帧咔嚓
          } else {
            // 走到了才发现空架：confused＋犯困（咖啡歇是能量备胎，自然接手）
            board.emoter.emote(a.name, 'confused');
            board.emoter.emote(a.name, 'sleepy');
            this.finishEat(a);
          }
        }
        break;
      case 'eat':
        if (st.t >= 2.5) { // 吃 2.5s
          const gain = this.en(a.name) < EPARAMS.critAt && EPARAMS.drama.critEat
            ? EPARAMS.eatGain : EPARAMS.eatGain; // critEat 的 1.5x 加速留给动作帧
          this.energy.set(a.name, Math.min(EPARAMS.energyMax, this.en(a.name) + gain));
          a.holding = null;
          st.phase = 'toss';
          st.t = 0;
          // 丢垃圾：走向垃圾桶（geom.props 里 pantryBin 的脚印）
          const bin = this.binAnchor();
          if (bin) sendOnErrand(a, bin.x + 20, bin.y + 6, DIR.UP); // 摘锚再走——吃位 working 锚不摘人就钉在货架前
          else this.finishEat(a);
        }
        break;
      case 'toss':
        if (!a.hasTarget && !a.moving) { // 到桶边：丢
          const bin = this.binAnchor();
          if (bin && Math.hypot(a.x - bin.x, a.y - bin.y) <= EPARAMS.binTossRange + 20) {
            if (this.bin < EPARAMS.binCapacity) {
              this.bin++;
              board.emoter.emote(a.name, 'done');
            } else {
              // 满了：落地（桶周散落）
              this.dropLitter(bin.x + (Math.random() * 40 - 20), bin.y + 16 + Math.random() * 8, a.foodKey);
              board.emoter.emote(a.name, EPARAMS.drama.fullBinToss ? 'star' : 'sweat');
            }
          } else {
            // 没走到桶（被挤开等）：就地落地
            this.dropLitter(a.x + 8, a.y + 4, a.foodKey);
          }
          this.finishEat(a);
        }
        break;
    }
  }

  finishEat(a) {
    a.eating = null;
    a.holding = null;
    a.foodKey = '';
    a.hasTarget = false;
    a.working = false; // 摘锚散场（idle 落座/闲逛接管）
    a.idleT = 0.5 + Math.random() * 1.5;
  }

  // 垃圾桶锚点：geom.props 第 4 块（pantryBin 的脚印——服务端
  // PantryBlocks 顺序：armN/armE/stool0/stool1/bin/waterCtlr）
  binAnchor() {
    const g = this.board.art.geom;
    if (!g || !g.pantry || !g.pantry.props || g.pantry.props.length < 5) return null;
    const [x, y, w, h] = g.pantry.props[4];
    return { x: x + w / 2, y: y + h / 2 };
  }

  // 落地垃圾（软阻挡数据面：litter 数组的写入口）
  dropLitter(x, y, foodKey) {
    if (this.litter.length >= EPARAMS.litterMax) return; // 密度帽
    this.litter.push({ x, y, born: this.board.clock || 0, crumb: 'generic', foodKey: foodKey || '' });
  }

  // 保洁阿姨的清理接口（t_139 消费）：清桶/拾地/补货
  cleanBin() { this.bin = 0; }
  pickLitter(i) { if (i >= 0 && i < this.litter.length) this.litter.splice(i, 1); }
  restock() { this.snacks = EPARAMS.snackCapacity; }
  get snackLow() { return this.snacks <= EPARAMS.snackRestockAt; }

  // ── 渲染（画在演员之后、前景之前：落地垃圾与桶渐满都在地板层上）──
  paint(ctx) {
    // r_32.2 视态化：像素全在 paintState，paint 是实时适配腿。
    this.paintState(ctx, this.snapshot());
  }

  // snapshot：能量面的归一化视态——录制入帧／回放还原／实时绘制三方
  // 共用的单一真源形状：{bin, snacks, lt: [{x, y, age, foodKey}]}。
  snapshot() {
    const now = this.board.clock || 0;
    return {
      bin: this.bin,
      snacks: this.snacks,
      lt: this.litter.map((l) => ({ x: l.x, y: l.y, age: now - l.born, foodKey: l.foodKey || '' })),
    };
  }

  /** paintState：按归一化视态画（桶渐满／落地垃圾残骸／售罄纸条）。 */
  paintState(ctx, st) {
    const g = this.board.art.geom;
    if (!g || !st) return;
    // 桶渐满（3 档：平线→冒尖→溢出挂 1 件）
    const bin = this.binAnchor();
    if (bin) {
      const stage = st.bin >= EPARAMS.binCapacity ? 2
        : st.bin >= Math.ceil(EPARAMS.binCapacity / 2) ? 1 : 0;
      if (stage > 0) {
        ctx.fillStyle = '#a9b2bb'; // 桶口冒尖
        ctx.fillRect(bin.x - 5, bin.y - 10, 10, 4);
      }
      if (stage > 1) {
        ctx.fillStyle = '#fbfbf8'; // 溢出边缘挂一件
        ctx.fillRect(bin.x + 6, bin.y - 4, 5, 4);
      }
    }
    // 落地垃圾：t_136 残骸真形（条目 Res 字符画直绘）；目录缺席退占位形
    const FOODS = globalThis.__NIUMA_FOODS__ || [];
    for (const l of st.lt || []) {
      const age = l.age || 0;
      const fade = Math.max(0.2, 1 - Math.max(0, age - (EPARAMS.litterDecay - 10)) / 10);
      ctx.globalAlpha = fade;
      const f = l.foodKey ? FOODS.find((x) => x && x.key === l.foodKey) : null;
      if (f && f.res && f.res.length) {
        for (let ry = 0; ry < f.res.length; ry++) {
          for (let rx = 0; rx < f.res[ry].length; rx++) {
            const ch = f.res[ry][rx];
            if (ch === '.') continue;
            ctx.fillStyle = ch === '2' ? f.hex2 : f.hex;
            ctx.fillRect(Math.round(l.x - 5 + rx * 2), Math.round(l.y - 4 + ry * 2), 2, 2);
          }
        }
      } else {
        // 占位形（目录未载/无条目）：揉皱小团
        ctx.fillStyle = '#c9ccd2';
        ctx.fillRect(l.x - 2, l.y - 2, 4, 3);
        ctx.fillStyle = '#a9b2bb';
        ctx.fillRect(l.x - 1, l.y - 3, 2, 1);
      }
      ctx.globalAlpha = 1;
    }
    // 零食架售罄纸条（库存 0 时）
    if (st.snacks === 0 && g.pantry) {
      const [px, py] = [g.pantry.rect[0] + 40, g.pantry.rect[1] + 8];
      ctx.fillStyle = '#fffdf5';
      ctx.fillRect(px, py, 16, 7);
      ctx.fillStyle = '#f54a45';
      ctx.font = '5px system-ui, sans-serif';
      ctx.fillText(t('售罄'), px + 2, py + 5.5);
    }
  }
}
