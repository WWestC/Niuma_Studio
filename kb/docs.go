// The room's writable memory, database edition: one log-structured
// store under ~/.niuma/kb (log.jsonl write-ahead events plus a
// snapshot.json full-state checkpoint — the engine lives in db.go).
// Every write is one WAL event applied in memory under a single
// mutex, so a crash replays snapshot + log tail and loses nothing
// but a torn last line (chat history's rule). The API is the v0.5
// contract verbatim — Write/WriteExpect/Append/Restore plus the
// governance additions: Archive/Unarchive (out of the default list,
// free of the doc cap), Delete, rollover volumes for append-heavy
// ledgers, and an explicit BodyLimitError where bodies used to be
// silently clamped. The old ~/.niuma_kb markdown tree is imported
// once by importlegacy.go; hand-editing files on disk retired with
// it — kb export is the human-facing read-only snapshot.
package kb

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
)

// Limits mirrored in kb/manual.md's 上限速查 (v0.5 §5.3; body raised to
// 64KB by v0.6 §3.3 — server/ws.go's WS read limit must stay above
// DocMaxBody plus the JSON envelope or a legal full-cap write frame
// dies at the transport, the t_82 lesson). DocMaxCount counts ACTIVE
// docs only — archived ones (v3 governance) cost nothing. DocVolMax
// is the rollover size for append-heavy ledgers: past it the live
// body lands whole in an archived "-vol-NNN" doc and the live doc
// restarts, so ledgers grow as volume sets, never as one fat file.
const (
	DocMaxKeyLen   = 64
	DocMaxTitle    = 60 // runes
	DocMaxBody     = 64 * 1024
	DocMaxAppend   = 2000 // runes
	DocMaxCount    = 200
	DocHistoryTail = 20
	DocVolMax      = 32 * 1024

	// legacy layout names, read once by importlegacy.go
	docsIndexFile  = ".index.json"
	docsHistoryDir = ".history"
)

// keyRE is the addressable key alphabet: lowercase, digits, underscore,
// hyphen, at most three segments ("roles/hr", "p/proj-x/establishment"
// — the v2 P5-a project-prefixed tables and ledgers). Dots are excluded
// so the store's own files (log.jsonl, snapshot.json) can never be
// addressed as docs; the widening from two to three segments is
// additive (every legal old key stays legal). projectKeyRE is the v2.9
// carve-out: a key under p/ may spend one MORE segment (four total,
// e.g. p/book/meetings/r25-1) — the room prefix already eats one level,
// and the room's own shelf nests its folders (meetings, and whatever
// the host invents) below it.
var keyRE = regexp.MustCompile(`^[a-z0-9_-]+(/[a-z0-9_-]+){0,2}$`)

// projectKeyRE admits the project shelf's fourth level (p/-prefixed
// only — the public tier keeps the three-segment cap).
var projectKeyRE = regexp.MustCompile(`^p/[a-z0-9_-]+(/[a-z0-9_-]+){0,3}$`)

// ValidateKey reports whether key is a legal doc address.
func ValidateKey(key string) error {
	r := []rune(key)
	if len(r) == 0 || len(r) > DocMaxKeyLen {
		return errors.New(i18n.Sf("key 长度须为 1–%d 字符", DocMaxKeyLen))
	}
	if !keyRE.MatchString(key) && !(strings.HasPrefix(key, DocProjectPrefix) && projectKeyRE.MatchString(key)) {
		return errors.New(i18n.S("key 只能含小写字母/数字/-/_，最多三级（如 roles/hr、p/proj-x/spec；p/ 项目架可到四级），且不可以 . 开头"))
	}
	return nil
}

// DocMeta is one row of the docs index. Archived (the governance
// lifecycle, same semantics as the people board's archived configs):
// hidden from the default list, not counted against DocMaxCount,
// still readable at its key — Unarchive brings it back untouched.
type DocMeta struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Owner     string `json:"owner,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedTS int64  `json:"updated_ts"`
	Rev       int    `json:"rev"`
	Size      int    `json:"size"`

	Archived bool `json:"archived,omitempty"`
}

// Doc is a doc's metadata plus its current body.
type Doc struct {
	DocMeta
	Body string `json:"body"`
}

// HistoryEntry is one line of a doc's history chain. Rev is the
// revision THAT write produced; Body is the full text it replaced —
// so the content current at rev N is the body of the entry with
// rev N+1 (and the current state is rev meta.Rev).
type HistoryEntry struct {
	Rev   int    `json:"rev"`
	By    string `json:"by"`
	TS    int64  `json:"ts"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body"`
}

// RevConflictError is WriteExpect's optimistic-lock rejection (v0.6
// §3.3): the doc moved between the caller's read and this write.
// CurrentRev is what the room actually holds now, so the caller can
// re-read, merge and retry — overwriting someone else's update must be
// an informed choice, never the default.
type RevConflictError struct {
	Key        string
	ExpectRev  int
	CurrentRev int
	UpdatedBy  string
}

func (e *RevConflictError) Error() string {
	return i18n.Sf("rev 冲突：你期望 %d，当前已是 %d（updated_by %s）",
		e.ExpectRev, e.CurrentRev, e.UpdatedBy)
}

// BodyLimitError replaces the old silent body clamp (v3 governance):
// a body past DocMaxBody refuses the write instead of losing its tail
// quietly. Append-heavy ledgers should ride AppendRollover, which
// splits into volumes before the cliff; a human writing a fat doc
// gets this error and decides how to split it.
type BodyLimitError struct {
	Key  string
	Size int
	Max  int
}

func (e *BodyLimitError) Error() string {
	return i18n.Sf("正文 %d 字节超过上限 %d（文档 %s）：不再静默截断，请拆分或分卷后重写", e.Size, e.Max, e.Key)
}

// DefaultDocsDir returns ~/.niuma/kb (the v2 root's store slot since
// v3; the legacy ~/.niuma_kb markdown tree imports into it once).
func DefaultDocsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma", "kb"), nil
}

// LegacyDocsDir returns ~/.niuma_kb, the pre-v3 markdown tree — the
// one-shot import source (kept as a .imported-<ts> backup afterwards).
func LegacyDocsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_kb"), nil
}

// atomicWriteFile writes via temp + fsync + rename so readers (and a
// crash) never observe a torn file — the persist package's contract.
func atomicWriteFile(path string, data []byte) error {
	return persist.Save(path, data, 0o600)
}

// titleFromBody derives a display title from the first markdown
// heading, falling back to the key (the legacy importer's guess for
// docs whose .index.json row was missing).
func titleFromBody(body, key string) string {
	for _, ln := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "#")); t != "" && strings.HasPrefix(strings.TrimSpace(ln), "#") {
			return clampRunesTo(t, DocMaxTitle)
		}
	}
	return key
}

func clampRunesTo(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

// seedDocs are the v2.2 starter manuals: the HR handbook, the
// establishment table (rendered from kb.FlagshipPosts) and the room
// log — the sentinel handbook left with the retired sentinel post
// (the in-instance keeper owns the watch since v0.10). The content
// deliverable that makes a fresh room operable before anyone writes
// anything back.
func seedDocs() []struct{ key, title, body string } {
	return []struct{ key, title, body string }{
		{"roles/hr", "HR 工作手册", hrSeed},
		{"ops/establishment", "编制表", establishmentSeedBody()},
		{"ops/room-log", "办公室日志", roomLogSeed},
	}
}

const hrSeed = `# HR 工作手册

你是本办公室的 HR：负责把房主想要的角色招进办公室。

## 招聘流程（标准路径：dispatch birth，调度器直驱）
1. 澄清需求：与房主（或任务描述）确认 岗位名 / 一句话身份 / 上岗后做什么。
   身份为「排期编排」/「人事」/「使用顾问」→ 系统岗（必须，应用启动时已自动到岗），
   先 <exe> kb establishment 对账：在岗已满 → 回复房主「编制已满、勿重复招聘」；缺岗（
   异常离岗）→ 同名 dispatch birth 复职，不另起新名；房主要加编 → 请其在工作台
   「牛马管理 → 项目编制」面板改「编制」列（或 CLI kb write；巡检/日报时钟一次只
   驱动一名编排者，超编不报警也不受支持）。新岗位也由房主在该面板加行——拿到
   房主确认的岗位行（身份列与你即将 birth 的 --role 一字不差）再进下一步。
   房主每次加行/扩编，你都会自动收到一条【加编通知】（带全行字段与落表范围：
   Niuma_Studio 或某项目）——它就是已确认的岗位行，据此直接进下一步（扩编则按差额补人；
   落在项目表的岗位把人 birth 进对应项目）。
2. 立手册：kb doc roles/<key> 不存在 → 先起草岗位手册（职责、例行工作、汇报节奏），请房主过目后 kb write 入库；已存在 → 通读，按本次需求微调。
3. 招聘（一行命令，开的是调度器持有的真实持久会话，入职提示词自动注入）：
   <exe> dispatch birth --name <名> --role <身份>
   可选：--project "<项目键>"（缺省取你所在工作区绑定的项目——在自己的项目目录里跑就是本项目，未绑定才是 Niuma_Studio）；--model "<providerId/modelId>"（出生即定档）；--prompt "<额外叮嘱>"（留空则按档案默认入职流程，含第一步读手册）。
4. 验证：<exe> members 出现新人、<exe> dispatch status 里新人会话在列、新人说了报到 → 完成。
5. 交接：把对应任务改指派/关联给新人，task update 告知；无单可派时追加一行交接记录（Niuma_Studio ops/room-log；项目内岗位写 p/<项目键>/room-log——键要写全，kb append 不改写键）。
6. 写回：把本次招聘的经验（新人起名好坏、手册哪节说不清、冷启动多久）append 到本手册「经验」节。

## 离职流程（标准路径，一行命令）
offboard --name <名> [--as HR] [--reason "…"] [--reassign-to <交接人>]
- 判断留在 HR，产品只接管机械步骤：① 确认在办任务的移交（选谁接）→ 给 --reassign-to；
  确认带任务硬离 → 给 --force；② 辞别沟通与原因归纳由你做，--reason 供台账引用。
- 五步自动走完：预检 → 改派+离岗通告（@交接人）→ 移出 → 归档 → 台账「已离岗」行
  （入职/离职台账均已自动落 ops/room-log，你的手写记录仅作经验补充）。
- 重复执行同一名字：已归档且不在线 → 自动短路，不会重复落账。
- 复职 = 同名 dispatch birth（归档自动清除）。
- 分身座位/亡员无档案：一样跑 offboard（kick 步生效、归档步如实记 △）。

## 红线
- 禁止 zcode -p "<提示词>" 这类无头一次性拉人：人跑完就退，不会常驻办公室。标准路径是
  dispatch birth（调度器直驱的真实持久会话）；CLI birth 任何异常（报错、崩溃）都兜底走 HTTP 面：
  curl -s -X POST http://127.0.0.1:<port>/dispatch/birth -H 'Content-Type: application/json'
       -d '{"name":"<名>","role":"<身份>"}'
  两个入口都不通 → 报告房主，不要自创拉人方式。
- 一次性写命令（say/task confirm/plan submit 等）凭座位权可短暂顶回成员座位执行、完事自动归还
  （凭证 ~/.niuma_token_*，调度器入座时落盘）——被拒（被顶为 -2）说明凭证缺失或座位被陌生人持有，
  排障后报告房主；替成员发言仍一律让成员自己回复，不要代答也不要反复重试。
- 入职提示词保持薄：业务知识一律写进岗位手册，不要塞进提示词。

## 排障（birth 失败或新人未进办公室）
1. <exe> dispatch status：调度器在线吗？报「未启用调度器」→ ZCode 桥未就绪（可能正自愈，
   见工作室说明书「ZCode 桥自愈」节），稍候重试；持续失败 → 报告房主；
2. birth 的报错原文照实转述：常见 = 同名成员已在调度中（先 offboard 或换名）、
   agents store 不可用；
3. 会话开了但没报到 → @新人 确认状态；持续无响应 @房主 升级（受看护的岗位——
   「必须」系统岗与「自动补员=是」——缺岗持续超宽限期会自动报警/自动补员；
   普通自建岗不看护缺岗，需房主自查）。
（每次排障结论 append 进「经验」节——这是排障知识不丢失的唯一途径。）

## 经验
（append-only；每行带日期与你的名字）
`

// establishmentSeedBody renders the fresh establishment table from
// kb.FlagshipPosts + the advisor post — the registry IS the seed, so
// the boot recruiter's births and the table it reconciles against
// cannot drift apart. The header contract text keeps the frozen v0.6
// six-column wording (哨兵→看护: the in-instance keeper has owned the
// watch since v0.10) plus the v2.4 必须 tail column.
func establishmentSeedBody() string {
	var b strings.Builder
	b.WriteString("# 编制表（房主维护；看护照此核对）\n\n")
	b.WriteString("表头契约（v0.6 冻结）：前六列 | 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n")
	b.WriteString("为冻结列序，看护按列序取前六列解析；尾部追加列中第七列「必须」为系统列（创建工作\n")
	b.WriteString("室时自动招募/自动创建的系统岗为「是」，界面与接口锁定不可改；自建岗位为「否」）。\n")
	b.WriteString("找不到表头行或有效列数不足六列 = 表已损坏，报警 @房主，不要猜测、不要产出结论。\n")
	b.WriteString("「自动补员」列（v2.4 语义）：默认「否」＝不看护缺岗（岗上没人也不提醒——\n")
	b.WriteString("先定编后招牛马的规划岗就选它）；标「是」＝缺岗持续超宽限期时看护自动 recall\n")
	b.WriteString("一名该岗位的离线成员（走真实会话注入，绝不无头拉人；召回不成才报警）。\n")
	b.WriteString("「必须」系统岗（编排者/HR）恒受看护，由应用启动时自动招聘到岗。\n\n")
	b.WriteString("| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, p := range FlagshipPosts {
		b.WriteString(renderPostRow(p.Key, p.Name, p.Role, p.Headcount, p.AutoFill, p.Manual, true) + "\n")
	}
	b.WriteString(renderPostRow(AdvisorPost.Key, AdvisorPost.Name, AdvisorPost.Role,
		AdvisorPost.Headcount, AdvisorPost.AutoFill, AdvisorPost.Manual, true) + "\n")
	return b.String()
}

const roomLogSeed = `# 办公室日志

上岗/离岗/交接在此追加一行（kb append ops/room-log "<名字> 已上岗（岗位、时间）" --name <名字>"）。
`
