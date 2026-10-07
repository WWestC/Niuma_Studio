package kb

// Establishment reconciliation (v0.8 M3 / t_12): one derived view over
// the ops/establishment doc — parse the frozen six-column header
// contract (v0.6 M3), match present members by exact role text, and
// diff headcount. A broken table is a VISIBLE event, never a guessed
// one: parse errors return an empty rows list plus a parse_error the
// CLI turns into an alarm — the watch keeper's "宁可误报缺岗，不可漏报"
// stance (PRD §12-5).

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// establishmentColumns is the frozen header contract (v0.6 M3): the
// first six columns in order; trailing columns are allowed additions
// and ignored. The one trailing column the contract itself defines is
// 必须 (the seventh, v2.4): system posts — 编排者/HR/小助手 — carry 是,
// everyone else 否; the column is code-governed, the UI and the row API
// refuse to set it on custom posts.
var establishmentColumns = [6]string{"岗位 key", "名称", "身份", "编制", "自动补员", "手册"}

// EstablishmentRow is one reconciled post: the table's fields plus who
// of the room actually fills it (by exact role match) and the diff.
type EstablishmentRow struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Headcount int      `json:"headcount"`
	AutoFill  bool     `json:"auto_fill"`
	Manual    string   `json:"manual"`
	Must      bool     `json:"must"`             // 第七列「必须」：系统岗=是（创建时自动招募/自动创建，界面锁定）
	System    bool     `json:"system,omitempty"` // 由代码判定（key ∈ 系统岗注册表），报告面携带给 UI 置灰
	Present   []string `json:"present"`
	Missing   int      `json:"missing"`
}

// EstablishmentReport is the /kb/establishment payload. ParseError is
// empty on success; when it is not, Rows is empty too — callers get a
// judgeable failure, never a table of guesses. Rev is the doc's current
// revision (0 with a parse error or a missing doc) — the optimistic
// write anchor the row API's expect_rev drinks from.
type EstablishmentReport struct {
	Rows       []EstablishmentRow `json:"rows"`
	ParseError string             `json:"parse_error"`
	Rev        int                `json:"rev"`
}

// Establishment parses the establishment doc and reconciles it against
// the room: present = online members (ghost seats included) and active
// configs whose role text equals the row's 身份 column EXACTLY — role
// drift ("测试" vs "测试工程师") reads as missing by design; the manual
// requires the table and onboarding --role to match character for
// character, and aliasing stays out until it proves necessary (§12-5).
func Establishment(r Roster, shelf ConfigShelf, docs *DocsStore) EstablishmentReport {
	doc, err := docs.Get("ops/establishment", 0)
	if err != nil {
		return EstablishmentReport{ParseError: i18n.Sf("编制表文档不可读：%v（用 kb write ops/establishment 重建，表头需为冻结六列）", err)}
	}

	// present candidates by exact role: live members win, active
	// configs fill the offline seats; archived ones are gone by design.
	roleHolders := map[string][]string{} // role text → member names (ordered, deduped)
	seen := map[string]bool{}
	for _, m := range r.Members() {
		if m.Role != "" && !seen[m.Name] {
			seen[m.Name] = true
			roleHolders[m.Role] = append(roleHolders[m.Role], m.Name)
		}
	}
	if shelf != nil {
		for _, c := range shelf.WireListFiltered(false) {
			if c.Role != "" && !seen[c.Name] {
				seen[c.Name] = true
				roleHolders[c.Role] = append(roleHolders[c.Role], c.Name)
			}
		}
	}

	parsed, perr := EstablishmentRows(doc.Body)
	if perr != "" {
		return EstablishmentReport{ParseError: perr}
	}
	rows := make([]EstablishmentRow, 0, len(parsed))
	for _, r := range parsed {
		reconcileRow(&r, roleHolders)
		rows = append(rows, r)
	}
	OrderReportRows(rows)
	return EstablishmentReport{Rows: rows, Rev: doc.Rev}
}

// OrderReportRows reorders one reconciled report for every display
// face (the GET endpoints, the row API's answer, the CLI table): the
// advisor post (小助手) sits at the very top, the remaining system
// posts follow in FlagshipPosts registry order (编排者, HR), and the
// host's custom rows keep the doc's own line order (stable sort). The
// doc itself is never reordered — the table's line order is the
// host's authoring; only the report faces present it this way.
func OrderReportRows(rows []EstablishmentRow) {
	rank := func(r EstablishmentRow) int {
		if r.Key == AdvisorPost.Key {
			return 0
		}
		for i, p := range FlagshipPosts {
			if r.Key == p.Key {
				return 1 + i
			}
		}
		return 1 + len(FlagshipPosts)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rank(rows[i]) < rank(rows[j]) })
}

// reconcileRow stamps one parsed row for the report faces: system rows
// carry their code-known 必须 (a hand-stripped column cannot demote
// them) and the advisor row is exempt from presence diffing — its life
// is the assistant session's own, not a seat the roster could count.
func reconcileRow(r *EstablishmentRow, roleHolders map[string][]string) {
	r.System = SystemPostKey(r.Key)
	if r.System {
		r.Must = true
	}
	if r.Key == AdvisorPost.Key {
		r.Present, r.Missing = nil, 0
		return
	}
	r.Present = append([]string(nil), roleHolders[r.Role]...)
	if r.Missing = r.Headcount - len(r.Present); r.Missing < 0 {
		r.Missing = 0
	}
}

// ReconcileRows is the report faces' shared post-parse step (the server
// package's twin of Establishment's own loop): stamp system 必须, exempt
// the advisor, diff presence by exact role.
func ReconcileRows(rows []EstablishmentRow, roleHolders map[string][]string) {
	for i := range rows {
		reconcileRow(&rows[i], roleHolders)
	}
}

// EstablishmentRows parses a establishment-table body under the frozen
// six-column contract and returns the rows WITHOUT any presence
// reconciliation — the shared parse layer for the /kb/establishment
// endpoint (which adds the roster) and the room's watch keeper (which
// adds its own live-seat view). ParseError is empty on success; rows
// are nil with it set — a visible failure, never a table of guesses.
func EstablishmentRows(body string) ([]EstablishmentRow, string) {
	rows := []EstablishmentRow{}
	headerSeen := false
	for lineNo, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "|") {
			continue
		}
		cells := splitTableRow(line)
		if len(cells) < len(establishmentColumns) {
			if headerSeen {
				// a data row with fewer than six columns is a broken
				// table, not a skipped one
				return nil, brokenTableText(lineNo)
			}
			continue // still looking for the header
		}
		head := cells[:len(establishmentColumns)]
		if !headerSeen {
			if !isHeaderRow(head) {
				continue
			}
			headerSeen = true
			continue
		}
		if isSeparatorRow(head) {
			continue
		}
		hc, err := strconv.Atoi(strings.TrimSpace(head[3]))
		if err != nil {
			return nil, i18n.Sf(
				"编制表第 %d 行编制数不是整数（%q）——请修正后重试；解析失败不产出结论", lineNo+1, head[3])
		}
		autoFill := false
		switch strings.TrimSpace(head[4]) {
		case "是":
			autoFill = true
		case "否":
		default:
			return nil, i18n.Sf(
				"编制表第 %d 行自动补员列须为「是/否」（%q）——请修正后重试；解析失败不产出结论", lineNo+1, head[4])
		}
		// 第七列「必须」是契约定义的唯一尾列：缺席=否（六列旧行照常
		// 解析），在场则必须是 是/否；再往后的尾列是别人的自由追加，
		// 一律忽略。
		must := false
		if len(cells) >= len(establishmentColumns)+1 {
			switch strings.TrimSpace(cells[len(establishmentColumns)]) {
			case "是":
				must = true
			case "否":
			default:
				return nil, i18n.Sf(
					"编制表第 %d 行必须列须为「是/否」（%q）——请修正后重试；解析失败不产出结论", lineNo+1, cells[len(establishmentColumns)])
			}
		}
		rows = append(rows, EstablishmentRow{
			Key:       strings.TrimSpace(head[0]),
			Name:      strings.TrimSpace(head[1]),
			Role:      strings.TrimSpace(head[2]),
			Headcount: hc,
			AutoFill:  autoFill,
			Manual:    strings.TrimSpace(head[5]),
			Must:      must,
		})
	}
	if !headerSeen {
		return nil, i18n.Sf(
			"编制表未找到冻结表头行（| %s |）——表可能被改坏；请 @房主 报警，不要猜测", strings.Join(establishmentColumns[:], " | "))
	}
	return rows, ""
}

// brokenTableText reports a post-header row that cannot hold six
// columns.
func brokenTableText(lineNo int) string {
	return i18n.Sf("编制表第 %d 行有效列数不足六列——表可能被改坏；请 @房主 报警，不要猜测", lineNo+1)
}

// EstablishmentRole resolves one table row's 身份 by post key — the
// dispatch birth --post path's single source（岗位锚的分项目根治：HR
// 不再手抄身份串，锚到的行即真相）. ok=false on a missing/broken
// table or an unknown post key: the caller falls back to whatever role
// it was given, never guesses.
func EstablishmentRole(docs *DocsStore, docKey, postKey string) (role string, ok bool) {
	postKey = strings.TrimSpace(postKey)
	if postKey == "" {
		return "", false
	}
	doc, err := docs.Get(docKey, 0)
	if err != nil {
		return "", false
	}
	rows, perr := EstablishmentRows(doc.Body)
	if perr != "" {
		return "", false
	}
	for _, r := range rows {
		if r.Key == postKey {
			return r.Role, true
		}
	}
	return "", false
}

// splitTableRow splits one markdown table line into trimmed cells
// (leading/trailing pipes dropped).
func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// isHeaderRow reports whether the six cells equal the frozen contract.
func isHeaderRow(cells []string) bool {
	for i, want := range establishmentColumns {
		if cells[i] != want {
			return false
		}
	}
	return true
}

// isSeparatorRow matches |---|---| style separators under the header.
func isSeparatorRow(cells []string) bool {
	dashes := 0
	for _, c := range cells {
		if c == "" {
			continue
		}
		if strings.Trim(c, "-: ") == "" && strings.Contains(c, "-") {
			dashes++
		} else {
			return false
		}
	}
	return dashes > 0
}

// --- post self-registration (v2) ------------------------------------------

// EnsureEstablishmentPost registers one post row in a scope's
// establishment table — the orchestrator birth's self-registration: the
// post joins the keeper's watch the moment a person takes the seat. A
// missing table is CREATED under the frozen six-column contract (plus
// the 必须 tail column) with just this row; an existing table gains the
// row inserted after its last table line (trailing prose untouched —
// markdown renders the table contiguously); a row whose 岗位 key already
// exists is left exactly as the host wrote it (idempotent rebirth/recall).
// A broken table is refused, never edited on a guess — the keeper's §12-5
// stance. Writes ride WriteExpect's optimistic lock, retried a bounded
// three times against a concurrent host edit. Returns whether the
// store was actually written.
func EnsureEstablishmentPost(docs *DocsStore, docKey, postKey, name, role string, headcount int, autoFill bool, manual string, must bool, by string) (bool, error) {
	row := renderPostRow(postKey, name, role, headcount, autoFill, manual, must)
	for attempt := 0; attempt < 3; attempt++ {
		doc, err := docs.Get(docKey, 0)
		if errors.Is(err, os.ErrNotExist) {
			if _, werr := docs.WriteExpect(docKey, "编制表", freshEstablishmentBody(row), by, 0); werr == nil {
				return true, nil
			} else if !isRevConflict(werr) {
				return false, werr
			}
			continue // created meanwhile: fall through to the append path
		}
		if err != nil {
			return false, err
		}
		rows, perr := EstablishmentRows(doc.Body)
		if perr != "" {
			return false, errors.New(i18n.Sf("编制表 %s 损坏，拒绝自动登记（%s）——请 @房主 修表后再入职", docKey, perr))
		}
		for _, r := range rows {
			if r.Key == postKey {
				return false, nil // already governed: never rewrite the host's row
			}
		}
		body, ok := appendPostRow(doc.Body, row)
		if !ok {
			return false, errors.New(i18n.Sf("编制表 %s 未找到冻结表头行，拒绝自动登记", docKey))
		}
		if _, werr := docs.WriteExpect(docKey, doc.Title, body, by, doc.Rev); werr == nil {
			return true, nil
		} else if !isRevConflict(werr) {
			return false, werr
		}
	}
	return false, errors.New(i18n.Sf("编制表 %s 登记与房主编辑冲突，重试耗尽——稍后 recall/复职会再试", docKey))
}

// renderPostRow formats one data row under the frozen contract plus
// the 必须 tail column.
func renderPostRow(postKey, name, role string, headcount int, autoFill bool, manual string, must bool) string {
	fill := "否"
	if autoFill {
		fill = "是"
	}
	mustCell := "否"
	if must {
		mustCell = "是"
	}
	return fmt.Sprintf("| %s | %s | %s | %d | %s | %s | %s |", postKey, name, role, headcount, fill, manual, mustCell)
}

// establishmentHeader is the canonical table head the seed, the fresh
// creates and the rebuild all drink from: the frozen six plus 必须.
func establishmentHeader() string {
	return "| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|"
}

// freshEstablishmentBody is a brand-new table: the header kept in
// lockstep with establishmentSeedBody's own block, plus the one row.
func freshEstablishmentBody(row string) string {
	return "# 编制表（房主维护；看护照此核对）\n\n" +
		establishmentHeader() + "\n" +
		row + "\n"
}

// RebuildEstablishmentBody rewrites one establishment doc's table block
// from parsed rows — the shared writer behind the row API and the 必须
// migration. Prose before the header and after the last table line is
// preserved byte-for-byte; the table itself always comes back in the
// canonical seven-column shape (the frozen six plus 必须). The caller
// must have parsed the body successfully already; ok=false means no
// header was found — refuse rather than guess.
func RebuildEstablishmentBody(body string, rows []EstablishmentRow) (string, bool) {
	lines := strings.Split(body, "\n")
	headerIdx, last := -1, -1
	for i, line := range lines {
		if !strings.Contains(line, "|") {
			continue
		}
		cells := splitTableRow(line)
		if headerIdx < 0 {
			if len(cells) >= len(establishmentColumns) && isHeaderRow(cells[:len(establishmentColumns)]) {
				headerIdx, last = i, i
			}
			continue
		}
		last = i
	}
	if headerIdx < 0 {
		return "", false
	}
	out := make([]string, 0, len(lines)+4)
	out = append(out, lines[:headerIdx]...)
	out = append(out, strings.Split(establishmentHeader(), "\n")...)
	for _, r := range rows {
		out = append(out, renderPostRow(r.Key, r.Name, r.Role, r.Headcount, r.AutoFill, r.Manual, r.Must))
	}
	out = append(out, lines[last+1:]...)
	return strings.Join(out, "\n"), true
}

// ValidatePostRow judges one host-authored row for the write faces (the
// row API and nothing else — the system rows come from code, never the
// wire). A refusal's text is the actionable reason.
func ValidatePostRow(r EstablishmentRow) error {
	if SystemPostKey(r.Key) {
		return errors.New(i18n.Sf("岗位 key「%s」是系统岗（必须），由系统维护、不可自建", r.Key))
	}
	if r.Key == "" || len([]rune(r.Key)) > 32 {
		return errors.New(i18n.S("岗位 key 必填且 ≤32 字符"))
	}
	if strings.ContainsAny(r.Key, "|\n") || strings.ContainsAny(r.Key, " \t") {
		return errors.New(i18n.S("岗位 key 不可含空格、制表符或竖线"))
	}
	if r.Name == "" || len([]rune(r.Name)) > 32 || strings.ContainsAny(r.Name, "|\n") {
		return errors.New(i18n.S("名称必填、≤32 字符且不含竖线"))
	}
	if r.Role == "" || len([]rune(r.Role)) > 32 || strings.ContainsAny(r.Role, "|\n") {
		return errors.New(i18n.S("身份必填、≤32 字符且不含竖线（需与出生 --role 一字不差）"))
	}
	if r.Headcount < 1 || r.Headcount > 50 {
		return errors.New(i18n.S("编制须在 1–50 之间"))
	}
	if len([]rune(r.Manual)) > 64 || strings.ContainsAny(r.Manual, "|\n") {
		return errors.New(i18n.S("手册列 ≤64 字符且不含竖线（填 roles/<key> 文档地址）"))
	}
	if r.Must {
		return errors.New(i18n.S("「必须」是系统岗专属（系统自动登记并招募——Niuma_Studio 随启动招聘，项目随开张自动招聘），自建岗位固定为否"))
	}
	return nil
}

// appendPostRow inserts row after the doc's LAST table line. The
// caller must have parsed the body successfully already (every
// post-header pipe line is then a table line); ok=false means no
// header was found — the parse precondition is violated, refuse.
func appendPostRow(body, row string) (string, bool) {
	lines := strings.Split(body, "\n")
	last := -1
	headerSeen := false
	for i, line := range lines {
		if !strings.Contains(line, "|") {
			continue
		}
		cells := splitTableRow(line)
		if !headerSeen {
			if len(cells) >= len(establishmentColumns) && isHeaderRow(cells[:len(establishmentColumns)]) {
				headerSeen = true
				last = i
			}
			continue
		}
		last = i
	}
	if !headerSeen || last < 0 {
		return "", false
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:last+1]...)
	out = append(out, row)
	out = append(out, lines[last+1:]...)
	return strings.Join(out, "\n"), true
}

// isRevConflict reports a WriteExpect rejection (the bounded-retry
// signal; everything else is a hard stop).
func isRevConflict(err error) bool {
	var rc *RevConflictError
	return errors.As(err, &rc)
}

// --- the 必须-column migration (v2.4) --------------------------------------

// UpgradeMustColumn converges every establishment table onto the
// seven-column shape: each row gains its 必须 cell (system posts 是，
// the rest keep their parsed value or default 否), the header is
// rewritten canonically, and the advisor row (小助手) is appended when
// the table never had one. Existing rooms with six-column tables are
// the target; a fresh seed already matches and writes nothing
// (idempotent — a rebuilt body equal to the current one skips the
// write). Broken tables are refused, never edited on a guess (§12-5);
// every rewrite rides WriteExpect with the same bounded retry as
// EnsureEstablishmentPost. Returns the doc keys actually rewritten.
func UpgradeMustColumn(docs *DocsStore) []string {
	if docs == nil {
		return nil
	}
	var touched []string
	for _, meta := range docs.List() {
		if !isEstablishmentDocKey(meta.Key) {
			continue
		}
		if ok, _ := upgradeMustRow(docs, meta.Key); ok {
			touched = append(touched, meta.Key)
		}
	}
	return touched
}

// upgradeMustRow rewrites one table; ok=false covers nothing-to-do and
// every refusal — the caller only reports rewrites (a refusal stays
// visible through the parse-alarm path, exactly like the sentinel
// retirement before it).
func upgradeMustRow(docs *DocsStore, docKey string) (bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		doc, err := docs.Get(docKey, 0)
		if err != nil {
			return false, err
		}
		rows, perr := EstablishmentRows(doc.Body)
		if perr != "" {
			return false, fmt.Errorf("编制表 %s 损坏，拒绝补列（%s）", docKey, perr)
		}
		hasAdvisor := false
		for i := range rows {
			if SystemPostKey(rows[i].Key) {
				rows[i].Must = true // the registry is the truth a hand edit cannot demote
			}
			if rows[i].Key == AdvisorPost.Key {
				hasAdvisor = true
			}
		}
		if !hasAdvisor {
			rows = append(rows, EstablishmentRow{
				Key: AdvisorPost.Key, Name: AdvisorPost.Name, Role: AdvisorPost.Role,
				Headcount: AdvisorPost.Headcount, AutoFill: AdvisorPost.AutoFill,
				Manual: AdvisorPost.Manual, Must: true,
			})
		}
		body, ok := RebuildEstablishmentBody(doc.Body, rows)
		if !ok {
			return false, fmt.Errorf("编制表 %s 未找到冻结表头行，拒绝补列", docKey)
		}
		if body == doc.Body {
			return false, nil // already converged: idempotent
		}
		if _, werr := docs.WriteExpect(docKey, doc.Title, body, "迁移", doc.Rev); werr == nil {
			return true, nil
		} else if !isRevConflict(werr) {
			return false, werr
		}
	}
	return false, fmt.Errorf("编制表 %s 补列与房主编辑冲突，重试耗尽——下次启动会再试", docKey)
}
