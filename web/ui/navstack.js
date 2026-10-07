// navstack.js — 板块导航的前进/回退栈（ZCode 式工具栏 ←→ 的腿）。
// 零 DOM 依赖（不碰 location——back()/forward() 只把目标 hash 交还
// 调用方去导航，模块只管记账），node:test 直测。
//
// 记账纪律（note 是唯一入口，route() 每次落位后喂它）：
//   · 栈顶与来者同 hash → 忽略（route() 会被无 hash 变化的调用重入，
//     boot 期 616/681 两次 route() 就是这么来的）；
//   · 栈顶与来者同板块（hash 斜杠前段，如 #/kb/chronicle 与
//     #/kb/docs）→ 替换栈顶——历史按板块粒度记，板块内页签深链
//     算「同一站」，回退一步跨板块、前进回来落在离开时的子页签；
//   · 其余 → 截断指针后的前向记录再 push（浏览器同款：回退后走
//     新路，旧前程作废）。
// back()/forward() 弹目标前记一笔 pending，紧随的每个 note(hash)
// 消费一笔、只广播亮灭不记账——程序性移动与用户导航就分开了。
// pending 是计数不是布尔：连点两次 back() 排队两个 hashchange，
// 各消费各的，不会把第二个程序性落位错记成用户 push。浏览器侧
// 键/触控板手势的后退会以普通 note 进栈（多记一条不影响正确性，
// 那是「应用外」的导航，按钮口径仍自洽）。

/** hash 的板块头：'#/kb/chronicle' → 'kb'，'' → ''。 */
function headOf(hash) {
  return String(hash || '').replace(/^#?\/?/, '').split('/')[0];
}

export class NavStack {
  /**
   * @param {(canBack: boolean, canFwd: boolean) => void} [onChange]
   *   亮灭广播（←→ 小钮的 disabled 同步）——栈每次变动后回调一次。
   */
  constructor(onChange) {
    this.stack = [];
    this.pos = -1;
    this.pending = 0;
    this.onChange = onChange;
  }

  get canBack() { return this.pos > 0; }
  get canFwd() { return this.pos >= 0 && this.pos < this.stack.length - 1; }

  #emit() { this.onChange?.(this.canBack, this.canFwd); }

  /**
   * 落一笔导航记录（route() 每次落位后调用；hash 建议传归一值
   * location.hash || '#/room'——空 hash 与 #/room 是同一站）。
   * @param {string} hash @returns {boolean} 是否改了账（测试观察用） */
  note(hash) {
    if (this.pending > 0) { // back()/forward() 引起的程序性落位：移指针已在先
      this.pending--;
      this.#emit();
      return false;
    }
    if (this.pos >= 0 && this.stack[this.pos] === hash) { this.#emit(); return false; }
    if (this.pos >= 0 && headOf(this.stack[this.pos]) === headOf(hash)) {
      this.stack[this.pos] = hash; // 同板块子路径：栈顶换成最新页签
      this.#emit();
      return true;
    }
    this.stack = this.stack.slice(0, this.pos + 1);
    this.stack.push(hash);
    this.pos++;
    this.#emit();
    return true;
  }

  /**
   * 回退一步。
   * @returns {string|null} 目标 hash（调用方去 location.hash 导航）；
   *   无路可回返回 null（按钮灰着，这应是防守位） */
  back() {
    if (!this.canBack) return null;
    this.pos--;
    this.pending++;
    return this.stack[this.pos];
  }

  /**
   * 前进一步。
   * @returns {string|null} 目标 hash；无路可进返回 null */
  forward() {
    if (!this.canFwd) return null;
    this.pos++;
    this.pending++;
    return this.stack[this.pos];
  }
}
