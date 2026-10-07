package server

// chronicle.go — the 工作室志's auto-collection hooks (r_10, t_152):
// three sources append one chronicle line each — task done (链闭环与
// 里程碑单), req split (需求立顶) and the rebuild-successful git commit.
// Everything rides the existing kb append channel (the append-only
// discipline 小鹿's t_150 体例 locked) — no new storage, no schema, and
// the hooks NEVER kb-write an existing doc (the overwrite accident that
// cost t_151 its 25 backfill entries is now a written rule; the one
// Write left is the birth-if-missing ceremony below).
//
// Per-office scoping: every line lands in its event's room journal —
// kb.ChronicleDocKey maps the lobby to the v1 ops/chronicle and a
// project to p/<key>/chronicle, so one office's blackboard never
// shows another office's ledger. The rebuild commit is studio-wide
// and stays in the lobby's journal.
//
// 体例 (design/chronicle §一/§四)：一行一条「YYYY-MM-DD｜类型｜人｜
// 一句话」；人取事件主语（task 的 assignee / req 的编排者 / git 的
// 提交者），钩子不署自己；同源同键去重（task 号 / 需求号 / commit
// hash）走内存环 100；钩子侧无法判定的（链闭环 vs 普通单）宁录不漏，
// 由编排者巡检校对划线——business as usual, chronicle is a ledger
// not a gate.

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/util"
)

// chronicleKey is the ledger doc t_151's backfill established — the
// lobby's journal. Project journals live at kb.ChronicleDocKey.
const chronicleKey = "ops/chronicle"

// Chronicle is the hook hub: one instance per Server, safe for
// concurrent use (task events can land from any room's engine).
type Chronicle struct {
	docs *kb.DocsStore

	mu   sync.Mutex
	seen map[string]struct{} // 同源同键去重环（键＝src:hash）
	ring []string            // 环序（最老在前，cap 100）
}

const chronicleRingCap = 100

// NewChronicle binds the hub to the docs store (nil docs = hooks all
// no-op — the chronicle is garnish, its absence is never an error).
func NewChronicle(docs *kb.DocsStore) *Chronicle {
	return &Chronicle{docs: docs, seen: map[string]struct{}{}}
}

// dedup records one key and reports whether this is its first sight
// (true = append-worthy；false = 已录过，静默跳过).
func (c *Chronicle) dedup(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[key]; ok {
		return false
	}
	c.seen[key] = struct{}{}
	c.ring = append(c.ring, key)
	if len(c.ring) > chronicleRingCap {
		delete(c.seen, c.ring[0])
		c.ring = c.ring[1:]
	}
	return true
}

// append writes one 体例 line into projectKey's journal. Failures are
// logged and swallowed — a chronicle miss must never break the business
// event it rides. AppendRollover (v3): past DocVolMax the live volume
// lands whole in an archived <key>-vol-NNN doc and the live doc
// restarts — the ledger grows as a volume set, never one fat file.
// A missing journal doc is born here (offboard.go's room-log ceremony):
// the lobby's ops/chronicle exists only where t_151's backfill ran, and
// a project's p/<key>/chronicle materializes on its first line — the
// one Write the hooks are allowed, and only ever on a doc that does
// not exist (never an overwrite).
func (c *Chronicle) append(projectKey, line string) {
	if c == nil || c.docs == nil {
		return
	}
	key := kb.ChronicleDocKey(projectKey)
	if _, err := c.docs.Get(key, 0); err != nil {
		if _, werr := c.docs.Write(key, "工作室志", "# 工作室志\n\n"+line, "chronicle"); werr != nil {
			log.Printf("chronicle: 建档失败（不影响业务事件）: %v", werr)
		}
		return
	}
	if _, err := c.docs.AppendRollover(key, line, "chronicle", 0); err != nil {
		log.Printf("chronicle: append 失败（不影响业务事件）: %v", err)
	}
}

// clamp60 压到体例的一句话帽（60 字）。
func clamp60(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 60 {
		r = r[:60]
	}
	return string(r)
}

// OnTaskDone — task done 事件钩子（挂 tasks.Update 的 done 转换之后）。
// 体例 §四·2：钩子无法判定「链闭环」时宁录不漏，巡检校对划线多余。
// projectKey 取任务的 ProjectKey（空 = 大厅）。
func (c *Chronicle) OnTaskDone(projectKey, taskID, title, assignee string) {
	if c == nil || !c.dedup("task:"+taskID) {
		return
	}
	who := strings.TrimSpace(assignee)
	if who == "" {
		who = "全室"
	}
	c.append(projectKey, fmt.Sprintf("%s｜交付｜%s｜完成 %s「%s」",
		time.Now().Format("2006-01-02"), who, taskID, clamp60(title)))
}

// OnReqSplit — 需求立顶钩子（挂 plan accept 的 MarkSplit 之后）。
// projectKey 取提案的 ProjectKey（提案只能提交给本办公室项目）。
func (c *Chronicle) OnReqSplit(projectKey, reqID, reqTitle, by string) {
	if c == nil || !c.dedup("req:"+reqID) {
		return
	}
	who := strings.TrimSpace(by)
	if who == "" {
		who = "全室"
	}
	c.append(projectKey, fmt.Sprintf("%s｜里程碑｜%s｜立顶需求 %s「%s」拆解入库",
		time.Now().Format("2006-01-02"), who, reqID, clamp60(reqTitle)))
}

// OnMerged — 分支并回主线钩子（挂 acceptMergeCore 的 merge 落地之
// 后，v2.8 gitflow）。体例 §四·1：里程碑行；人取批准合并的房主（或
// 代收的自动驾驶），refs 是提案快照里提交信息引用的台账号。projectKey
// 取合并提案自己的 ProjectKey（提案只能提交给本办公室项目）。
// OnReqPark / OnReqUnpark (r_31): 暂缓的进出留痕——编年史一行
// （park 带原因摘要，unpark 只记动作——原因在需求行上可追溯）。
func (c *Chronicle) OnReqPark(projectKey, reqID, reqTitle, note, by string) {
	if c == nil {
		return
	}
	who := strings.TrimSpace(by)
	if who == "" {
		who = "编排者"
	}
	noteStr := ""
	if note != "" {
		noteStr = "（" + clamp60(note) + "）"
	}
	c.append(projectKey, fmt.Sprintf("%s｜事件｜%s｜需求 %s「%s」转入暂缓%s",
		time.Now().Format("2006-01-02"), who, reqID, clamp60(reqTitle), noteStr))
}

func (c *Chronicle) OnReqUnpark(projectKey, reqID, reqTitle, by string) {
	if c == nil {
		return
	}
	who := strings.TrimSpace(by)
	if who == "" {
		who = "编排者"
	}
	c.append(projectKey, fmt.Sprintf("%s｜事件｜%s｜需求 %s「%s」解除暂缓（回 open 可拆解）",
		time.Now().Format("2006-01-02"), who, reqID, clamp60(reqTitle)))
}

func (c *Chronicle) OnMerged(m *merge.Merge, by string) {
	if c == nil || m == nil || !c.dedup("merge:"+m.ID) {
		return
	}
	who := strings.TrimSpace(by)
	if who == "" {
		who = "全室"
	}
	refs := ""
	if len(m.Tasks) > 0 {
		refs = "（" + strings.Join(m.Tasks, " ") + "）"
	}
	c.append(m.ProjectKey, fmt.Sprintf("%s｜里程碑｜%s｜分支 %s 并入主线 %s%s",
		time.Now().Format("2006-01-02"), who, m.Branch, m.Into, clamp60(refs)))
}

// OnRebuildCommit — rebuild 成功后的 git 提交钩子（RelaunchSelf 之前调）。
// 体例 §四·2：只收「rebuild 成功」那一笔（HEAD 即将上线的提交）——
// 摘要取 commit 首行冒号后的正文，去 type(scope) 前缀，压 60 字。
// rebuild 是全工作室事件：落大厅的 ops/chronicle（驾驶舱今日摘要读它）。
func (c *Chronicle) OnRebuildCommit() {
	if c == nil {
		return
	}
	out, err := util.HideConsole(exec.Command("git", "log", "-1", "--format=%H%n%s")).Output()
	if err != nil {
		return // 编年史缺席不挡 rebuild
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(parts) != 2 {
		return
	}
	hash, subject := parts[0], parts[1]
	if !c.dedup("git:" + hash) {
		return
	}
	// 摘要：type(scope): 摘要——取冒号后；无冒号则原样
	summary := subject
	if i := strings.Index(subject, ": "); i >= 0 {
		summary = subject[i+2:]
	}
	// 提交者（体例：人取事件主语）
	who, err2 := util.HideConsole(exec.Command("git", "log", "-1", "--format=%an")).Output()
	whoS := strings.TrimSpace(string(who))
	if err2 != nil || whoS == "" {
		whoS = "全室"
	}
	c.append("", fmt.Sprintf("%s｜交付｜%s｜落库上线：%s",
		time.Now().Format("2006-01-02"), whoS, clamp60(summary)))
}
