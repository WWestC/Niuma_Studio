// cleaner.js — t_139 保洁阿姨 NPC（r_07）：m-cleaner-cycle 常驻机制。
// 评审定案（r_07 评审会＋小鹿 r07-energy §四）：不建独立行为树，就是
// 一条巡逻循环——清桶 → 拾地面（逐垃圾点）→ 补货（库存 ≤2 顺手）→
// 巡逻间隔。cooldown 豁免：isNPC 永不触发「被发现」批评链、不进成员
// 名册不占编制（roomview 的成员装配循环不管她，这里自管生命周期）。
//
// 形象：ComposeSpec 固定（围裙走 scarf 挂点、发髻、打扫色系）——服
// 务端无档案，走老两维 URL＋固定色（#7a9e7a 打扫绿＋深发），atlas 缓
// 存键独立不与成员撞。台词池对齐 t_133 模式（role → 池）。
//
// 接入：RoomView 构造 CleanerRitual，step/paint 与其他 Ritual 同拍。
// 全天在岗（夜班照巡——成员没活也吃饭，夜里照样产垃圾；nightStop
// 拨 true 可恢复 0–6 点停巡，留作调参开关）。

import { Actor, DIR } from './actor.js';
import { EPARAMS } from './energy.js';
import { sfx } from './sfxport.js';
import { t } from '../ui/i18n.js';

// 巡逻节奏（r07-energy §四：5 分钟/轮）
export const CPARAMS = {
  patrolEvery: 300,   // 秒/轮
  nightStop: false,   // 全天在岗；true＝0–6 点停巡（调参开关）
  walkSpeed: 22,      // 阿姨走得慢（native px/s）
  pickDur: 1.6,       // 弯腰拾取一处的时长（A15 bend 一拍）
  idleRetry: 45,      // 空场重试（没活儿的轮次别干等整 5 分钟）
  pokeCd: 6,          // 点她冷却（防连点刷台词）
  pokeChainT: 1.2,    // 三连点窗口（秒）
  pokeBigCd: 30,      // 三连点欢呼的冷却
};

// 台词池（t_133 模式；阿姨是 NPC 没有画像标签，单池）——系统文案，t() 译
const LINES = {
  clean: [t('这桶都冒尖了。'), t('又是瓜子壳……'), t('擦一圈亮堂堂。'), t('垃圾不过夜哈。')],
  restock: [t('补货咯。'), t('零食架见底了。'), t('橘子新到的。'), t('这批饼干挺香。')],
  hum: [t('（哼小曲）'), t('（抹布声）'), t('（水桶哗啦）')],
  poke: [t('哎哟，扫着呢。'), t('别挡道哈~'), t('地刚拖的，靠边走。'), t('嗯？找我？')],
  cheer: [t('今儿地板亮得能照镜子！'), t('扫完这块就歇咯~')],
};

// 阿姨的固定形象（r_05 管线的两维调用——不走 staffing，无终身层档案；
// 打扫绿围裙感由 shirt 色承担，围裙细节走 scarf 需档案，这里用色块近似）。
// name/role 是名牌与台词署名（用户可见）——t() 译；actors 键两侧同用
// AUNTIE.name，en 模式整链一致。
const AUNTIE = { name: t('保洁阿姨'), shirt: '#7a9e7a', hair: '#3b2f2f', role: t('保洁') };

// 扫把手持件（t_139 UI 评审补钉）：阿姨是保洁，扫把不离手——进场即
// 持、退场随人消失。与打印纸/零食手持件同一绘制语言（fillRect 像素
// 块、native px、走路一拍 1px 摆）。横走时刷头落地在前脚、杆斜倚回
// 肩（经典扫地姿势）；背影/正面/弯腰拍立地靠身——弯腰拾掇时腾双手。
export const BROOM = {
  stick: '#a97c4b', stickD: '#82593a',   // 木杆双色调（亮棱＋暗棱）
  straw: '#d8a85e', strawD: '#b9854a',   // 稻草刷毛（梢部深一档）
  band: '#c9564a',                       // 缠扎红带
  skin: '#f0c8a0',                       // 握杆的手（与 poses.js 肤色约定一致）
};

// px：取整像素块（像素画不画半格，与 paintFoodHand 同纪律）
function px(ctx, x, y, w, h, c) {
  ctx.fillStyle = c;
  ctx.fillRect(Math.round(x), Math.round(y), w, h);
}

export class CleanerRitual {
  /** @param {import('./roomview.js').RoomView} board */
  constructor(board) {
    this.board = board;
    this.actor = null;       // 惰性装配的 Actor（首次巡逻时进场）
    this.phase = 'idle';     // idle | walk-bin | clean-bin | walk-litter | pick | walk-shelf | restock | leave
    this.t = 0;
    this.spot = null;        // 当前拾取目标的坐标（按坐标反查索引，防衰减错位）
    this.route = null;       // nav 路标链（当前腿——目标变了才重算）
    this.routeKey = '';
    this.patrolT = CPARAMS.patrolEvery * 0.4; // 首轮提前一点（开页 2 分钟内能见到）
    this.lineT = 0;
    this.pokeCd = 0;         // 点她冷却余额
    this.pokeChain = 0;      // 三连点计数
    this.pokeChainT = 0;     // 三连点窗口余额
  }

  // 深夜判定（与 M12 午睡同一时钟口径）
  nightNow() {
    const h = new Date().getHours();
    return CPARAMS.nightStop && h >= 0 && h < 6;
  }

  // 装配：一个普通 Actor，但 isNPC 标记隔离所有成员机制（busy/社交/
  // 体力/批评链都跳过 NPC——各引擎的资格链查 isNPC）
  spawn() {
    if (this.actor) return this.actor;
    const board = this.board;
    // 西门入场（bounds 挑入口点——阿姨不走成员的随机落位）
    const b = board.bounds();
    const x = b.minX + 20, y = (b.minY + b.maxY) / 2;
    const m = { name: AUNTIE.name, color: AUNTIE.shirt, hair: AUNTIE.hair, role: AUNTIE.role };
    const a = new Actor(m, x, y, board.art.atlas(AUNTIE.shirt, AUNTIE.hair, null));
    a.isNPC = true;           // 各引擎的豁免位（findCatches/forageBeat/beat 资格链）
    a.npcCleaner = true;
    board.actors.set(AUNTIE.name, a);
    this.actor = a;
    return a;
  }

  // 每帧节拍：巡逻计时＋阶段推进。dt 是真实帧时长（秒）——arrive 的
  // 步进直接吃它，不走近似。
  step(dt) {
    const board = this.board;
    const energy = board.energy;
    if (!energy) return;
    // 巡逻间隔（nightStop 默认关——全天上班夜班照巡）；脏乱召唤（彩
    // 蛋）：桶满或垃圾到帽，不等整轮计时，阿姨提前出场（「垃圾不过夜」的执行力）
    if (this.phase === 'idle') {
      if (this.nightNow()) return;
      this.patrolT -= dt;
      if (this.patrolT > 0 &&
        (energy.bin >= EPARAMS.binCapacity || energy.litter.length >= EPARAMS.litterMax)) {
        this.patrolT = 0;
      }
      if (this.patrolT <= 0) this.beginPatrol();
      return;
    }
    const a = this.actor;
    if (!a) { this.phase = 'idle'; return; }
    this.t += dt;
    this.dt = dt;
    // 点她冷却链（彩蛋）：冷却递减＋三连点窗口过期清零
    if (this.pokeCd > 0) this.pokeCd -= dt;
    if (this.pokeChainT > 0) {
      this.pokeChainT -= dt;
      if (this.pokeChainT <= 0) this.pokeChain = 0;
    }
    // 偶尔哼小曲（低频，巡逻中 8% 每 10s）
    this.lineT -= dt;
    if (this.lineT <= 0) {
      this.lineT = 10;
      if (this.phase.startsWith('walk') && Math.random() < 0.08) {
        board.say(a.name, LINES.hum[(Math.random() * LINES.hum.length) | 0],
          Math.floor(Date.now() / 1000), false, false, false);
      }
    }
    switch (this.phase) {
      case 'walk-bin': {
        const bin = energy.binAnchor();
        if (this.arrive(a, bin, 22)) {
          this.phase = 'clean-bin';
          this.t = 0;
          board.poser.pose(a.name, 'bend', 1.6);
          board.say(a.name, LINES.clean[(Math.random() * LINES.clean.length) | 0],
            Math.floor(Date.now() / 1000), false, false, false);
        }
        break;
      }
      case 'clean-bin':
        if (this.t >= CPARAMS.pickDur) {
          energy.cleanBin();
          this.phase = 'walk-litter';
          this.t = 0;
        }
        break;
      case 'walk-litter': {
        // 就近拾取：每一脚挑离当前最近的垃圾点（见 nextLitter）——拾尽
        // 后要补货才去零食架，没活儿不打卡
        const next = this.nextLitter();
        if (!next) {
          if (energy.snackLow) { this.phase = 'walk-shelf'; this.t = 0; }
          else this.finishPatrol();
          break;
        }
        if (this.arrive(a, next, 12)) {
          this.spot = next;
          this.phase = 'pick';
          this.t = 0;
          board.poser.pose(a.name, 'bend', CPARAMS.pickDur);
        }
        break;
      }
      case 'pick':
        if (this.t >= CPARAMS.pickDur) {
          if (this.spot) {
            // 按坐标反查索引：巡逻中途被衰减清掉的垃圾自然落空（找不
            // 到就跳过），不再需要「拾一个后面索引前移」的手工补偿
            const i = energy.litter.findIndex((l) =>
              l.x === this.spot.x && l.y === this.spot.y);
            if (i >= 0) energy.pickLitter(i);
          }
          sfx('scene-sweep'); // r_09：拾取完成刷刷（冷却 5s 兜连拾）
          this.spot = null;
          this.phase = 'walk-litter';
          this.t = 0;
        }
        break;
      case 'walk-shelf': {
        const shelf = this.shelfAnchor();
        if (!shelf) { this.finishPatrol(); break; }
        if (this.arrive(a, shelf, 20)) {
          this.phase = 'restock';
          this.t = 0;
          if (energy.snackLow) {
            board.say(a.name, LINES.restock[(Math.random() * LINES.restock.length) | 0],
              Math.floor(Date.now() / 1000), false, false, false);
          }
        }
        break;
      }
      case 'restock':
        if (this.t >= 1.2) {
          if (this.board.energy.snackLow) this.board.energy.restock();
          this.finishPatrol();
        }
        break;
      default: this.finishPatrol();
    }
  }

  // 房间切换时 roomview 会 actors.clear()——阿姨的引用也一并失效（下轮
  // 巡逻重新 spawn，phase 归 idle）。RoomView.setRoom 调这个。
  reset() {
    if (this.actor) this.actor = null;
    this.phase = 'idle';
    this.spot = null;
    this.route = null; this.routeKey = '';
    this.patrolT = Math.min(this.patrolT, CPARAMS.patrolEvery * 0.2);
  }

  beginPatrol() {
    const energy = this.board.energy;
    // 空场裁剪（巡线优化）：没桶没垃圾也不用补货——不进场不折腾（原
    // 来会门口闪进又闪出），缩短间隔等下一轮再看
    if (!energy.bin && !energy.litter.length && !energy.snackLow) {
      this.patrolT = CPARAMS.idleRetry;
      return;
    }
    this.spawn();
    // 空桶不去桶边打卡（巡线优化）：直接进拾垃圾腿
    this.phase = energy.bin > 0 ? 'walk-bin' : 'walk-litter';
    this.t = 0;
    this.spot = null;
    this.route = null; this.routeKey = '';
    this.actor.hasTarget = false;
  }

  finishPatrol() {
    // 退场：人撤出 actors（场景家具的高级形态——不占席不留影）
    if (this.actor) {
      this.board.actors.delete(AUNTIE.name);
      this.actor = null;
    }
    this.phase = 'idle';
    this.patrolT = CPARAMS.patrolEvery;
  }

  // 走位（巡线优化）：nav 就绪时走 A* 路标链绕开桌椅（路标链只在目标
  // 变化时重算），路标走空/不可达退回直奔终点——与 Actor 寻路「目标
  // 松弛到最近空格＋最后几 px 直线小跳」同一契约。nav 缺席（art 未就
  // 绪/测试环境）保持老直线走位。
  arrive(a, target, r) {
    if (!target) return true;
    if (Math.hypot(target.x - a.x, target.y - a.y) <= r) {
      a.moving = false;
      this.route = null; this.routeKey = '';
      return true;
    }
    let wp = target;
    const nav = this.board.nav;
    if (nav) {
      const key = `${Math.round(target.x)},${Math.round(target.y)}`;
      if (this.routeKey !== key || !this.route) {
        this.route = nav.path(a.x, a.y, target.x, target.y);
        this.routeKey = key;
      }
      while (this.route.length && Math.hypot(this.route[0].x - a.x, this.route[0].y - a.y) < 3) {
        this.route.shift();
      }
      if (this.route.length) wp = this.route[0];
    }
    const dx = wp.x - a.x, dy = wp.y - a.y;
    const d = Math.hypot(dx, dy) || 1;
    // 让行（巡线礼让）：前方近处站着人就停这一拍——她的走位不吃
    // yieldTo（阿姨自己的 arrive 直线走），只有别人单方面让她；不停
    // 这一拍就是直穿对方身位。耐心上限 1.2s：被坐定的人（会议座就
    // 在门口漏斗边）挡死时等一等的期限，到点照旧通过——她是场景家
    // 具的高级形态，不能在漏斗里永久站住。只看航向前方 14px 锥内的
    // 人，侧后路过的不拦。
    const ux = dx / d, uy = dy / d;
    let blocked = false;
    for (const o of this.board.actors.values()) {
      if (o === a) continue;
      const rx = o.x - a.x, ry = o.y - a.y;
      const od = Math.hypot(rx, ry);
      if (od > 14 || od < 0.001) continue;
      if ((rx / od) * ux + (ry / od) * uy < 0.5) continue; // 不在前方锥内
      blocked = true;
      break;
    }
    if (blocked) {
      this.yieldT = (this.yieldT || 0) + (this.dt || 0.016);
      if (this.yieldT < 1.2) {
        a.moving = false;
        return false;
      }
    } else {
      this.yieldT = 0;
    }
    const stepLen = Math.min(CPARAMS.walkSpeed * (this.dt || 0.016), d);
    a.x += (dx / d) * stepLen;
    a.y += (dy / d) * stepLen;
    a.moving = true;
    a.animT += this.dt || 0.016;
    a.dir = Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? DIR.RIGHT : DIR.LEFT) : (dy > 0 ? DIR.DOWN : DIR.UP);
    return false;
  }

  // 就近拾取（巡线优化）：每一脚挑离当前最近的垃圾点——就近聚簇扫，
  // 不再按 y 全场排队来回横穿。拾掉的从 litter 数组消失，天然不回头。
  nextLitter() {
    const a = this.actor, L = this.board.energy.litter;
    if (!a || !L.length) return null;
    let best = null, bd = Infinity;
    for (const l of L) {
      const d = (l.x - a.x) * (l.x - a.x) + (l.y - a.y) * (l.y - a.y);
      if (d < bd) { bd = d; best = l; }
    }
    return best;
  }

  // 彩蛋：点她一下——回头搭话＋东张西望（冷却 pokeCd 防刷屏）；冷却
  // 中的连点静默计数，1.2s 窗口内攒满三下触发欢呼（大冷却）。台词走
  // 她自己的池（poke/cheer），动作走 poser。
  poke() {
    const a = this.actor;
    if (!a) return; // 不在场：点了空气
    const chained = this.pokeChainT > 0;
    if (!chained && this.pokeCd > 0) return;
    this.pokeChain = chained ? this.pokeChain + 1 : 1;
    this.pokeChainT = CPARAMS.pokeChainT;
    if (this.pokeChain >= 3) {
      this.pokeChain = 0;
      this.pokeCd = CPARAMS.pokeBigCd;
      this.board.poser.pose(a.name, 'cheer', 2.0);
      this.board.say(a.name, LINES.cheer[(Math.random() * LINES.cheer.length) | 0],
        Math.floor(Date.now() / 1000), false, false, false);
      sfx('scene-pop', { force: true });
      return;
    }
    if (this.pokeCd > 0) return; // 连点在攒——不出声只计数
    this.pokeCd = CPARAMS.pokeCd;
    this.board.poser.pose(a.name, 'lookaround');
    this.board.say(a.name, LINES.poke[(Math.random() * LINES.poke.length) | 0],
      Math.floor(Date.now() / 1000), false, false, false);
  }

  // 零食架锚点： pantry 北臂西端上方（与 interact.js SPOTS.snack 同源）
  shelfAnchor() {
    return { x: 251 * 2, y: 85 * 2 };
  }

  // paintBroom：阿姨的扫把（board 的 draw 每帧对 npcCleaner 调用）。
  // 自护栏：非保洁 NPC 不画。a 是 Actor（脚锚 x/y、朝向 dir、走路拍
  // animT/moving），画在 sprite 之上、前景层之下——桌面正确遮挡。
  paintBroom(ctx, a) {
    if (!a || !a.npcCleaner) return;
    const step = a.moving ? (Math.floor(a.animT * 6) % 2 ? 1 : 0) : 0;
    // 立式（背影/正面/弯腰拍）：杆立脚边、刷头落地
    if (a.dir === DIR.UP || a.dir === DIR.DOWN ||
      this.phase === 'pick' || this.phase === 'clean-bin') {
      const sx = a.x + 9 + step;
      px(ctx, sx, a.y - 33, 1, 27, BROOM.stick);      // 杆亮棱
      px(ctx, sx + 1, a.y - 33, 1, 27, BROOM.stickD); // 杆暗棱
      px(ctx, sx - 1, a.y - 24, 4, 3, BROOM.skin);    // 握杆的手（胯高）
      px(ctx, sx - 3, a.y - 8, 8, 2, BROOM.band);     // 缠扎红带
      px(ctx, sx - 3, a.y - 6, 8, 3, BROOM.straw);    // 刷毛上窄
      px(ctx, sx - 4, a.y - 3, 10, 3, BROOM.straw);   // 刷毛下宽（喇叭口）
      px(ctx, sx - 3, a.y, 8, 1, BROOM.strawD);       // 刷毛梢贴地
      return;
    }
    // 横走式：刷头落地在前脚、杆斜倚回肩——四段阶梯拼斜杆，顶随步拍
    // 轻摆（手持件的摆动语言）
    const s = a.dir === DIR.LEFT ? -1 : 1;
    const hx = a.x + s * 13, hy = a.y - 7;            // 刷头中心（落地）
    px(ctx, hx - 3, hy - 1, 6, 2, BROOM.straw);       // 刷毛上窄
    px(ctx, hx - 5, hy + 1, 10, 4, BROOM.straw);      // 刷毛下宽
    px(ctx, hx - 4, hy + 5, 8, 2, BROOM.strawD);      // 刷毛梢
    px(ctx, hx - 5, hy - 3, 10, 2, BROOM.band);       // 缠扎红带
    for (let i = 0; i < 4; i++) {
      const bx = hx - 1 - s * i * 3 - s * step;       // 杆顶随步拍轻摆
      const by = a.y - 16 - i * 5;
      px(ctx, bx, by, 1, 7, BROOM.stick);             // 段亮棱
      px(ctx, bx + 1, by, 1, 7, BROOM.stickD);        // 段暗棱
      if (i === 1) px(ctx, bx, by + 2, 2, 3, BROOM.skin); // 手握杆中段
    }
  }
}
