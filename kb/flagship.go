package kb

// The flagship establishment (v2.2): the studio's two mandatory lobby
// posts — 编排者（排期编排）小牛 and HR（人事）小马, both Lv.8 — own the
// app's boot contract: the door opens only when every flagship post is
// seated (the dispatcher's boot recruiter births them the moment the
// ZCode bridge attaches; the splash shows the hiring progress). The
// registry here is the single truth the establishment seed table, the
// boot recruiter and the birth-time rank stamp all read — code and
// seed cannot drift. The v0.5 sentinel post is RETIRED with it: the
// in-instance keeper took over its watch in v0.10, so the post row
// leaves the seed and existing tables migrate once (RetireSentinelPosts).

import (
	"fmt"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/wire"
)

// FlagshipPost is one mandatory lobby post: the establishment row's
// six-column fields plus Person — the name the boot recruiter births
// into the seat — and Rank, the governance level the birth stamps.
type FlagshipPost struct {
	Key       string // 岗位 key（表第一列）
	Name      string // 岗位名称（表第二列）
	Role      string // 身份（与成员 role 一字不差）
	Person    string // 首任在岗人：启动招聘的出生名
	Manual    string // 岗位手册（空 = 编排者的冻结提示词自带）
	Headcount int
	AutoFill  bool
	Rank      int
}

// FlagshipPosts is the lobby's mandatory establishment, in table
// order: 编排者 小牛 first (the studio's scheduler), HR 小马 second
// (the studio's hiring desk). Both Lv.8 — peers under the host (99).
var FlagshipPosts = []FlagshipPost{
	{
		Key: "orchestrator", Name: "编排者",
		Role: orchestratorRole, Person: "小牛",
		Headcount: 1, AutoFill: true, Rank: orchestratorRank,
	},
	{
		Key: "hr", Name: "HR",
		Role: hrRole, Person: "小马",
		Manual:    "roles/hr",
		Headcount: 1, AutoFill: true, Rank: hrRank,
	},
}

// AdvisorPost is the establishment row for the 小助手 — the host's
// in-app usage advisor. It is a SYSTEM post (必须=是, locked in the UI)
// but NOT a flagship one: no dispatcher birth, no seat, no keeper
// watch — its life is the assistant session's own (born by the boot
// warm-up, adopted across boots via ~/.niuma/assistant.json). Presence
// reconciliation and the boot gate both skip its row; the table carries
// it so the 编制 surface shows the whole system roster in one place.
var AdvisorPost = FlagshipPost{
	Key: "assistant", Name: "小助手", Role: "使用顾问",
	Headcount: 1, AutoFill: false,
}

// ProjectSystemPosts is the system-post floor every ACTIVE project's
// establishment table must carry: the two flagship posts as
// PER-PROJECT instances — presence is counted from the project's own
// room only, so two projects' HRs (or 编排者) are necessarily separate
// people (phase-1's one-person-one-room rule does the separating) —
// plus the advisor, the ONE global system post: the same row in every
// table, the same single assistant session behind it, presence-exempt
// in every report face.
func ProjectSystemPosts() []FlagshipPost {
	out := make([]FlagshipPost, 0, len(FlagshipPosts)+1)
	out = append(out, FlagshipPosts...)
	out = append(out, AdvisorPost)
	return out
}

// EnsureProjectEstablishment converges one project's establishment
// table onto the ProjectSystemPosts floor: a missing table is CREATED
// with the three system rows (the 编制-is-mandatory doctrine — a
// project's table is no longer opt-in), an existing one gains
// whichever system rows it lacks (appended after its last table
// line; the host's own rows and their order are never touched).
// Idempotent — a converged table writes nothing; a broken table is
// refused, never edited on a guess (§12-5), and keeps the keeper's
// parse alarm as its visible face. The lobby is not this function's
// business: its table is the boot recruiter's (fixed first persons).
// Returns the post keys actually written.
func EnsureProjectEstablishment(docs *DocsStore, projectKey, by string) ([]string, error) {
	if docs == nil || projectKey == wire.LobbyKey {
		return nil, nil
	}
	docKey := EstablishmentDocKey(projectKey)
	var touched []string
	for _, p := range ProjectSystemPosts() {
		written, err := EnsureEstablishmentPost(docs, docKey,
			p.Key, p.Name, p.Role, p.Headcount, p.AutoFill, p.Manual, true, by)
		if err != nil {
			return touched, err
		}
		if written {
			touched = append(touched, p.Key)
		}
	}
	return touched, nil
}

// SystemPostKey reports whether key names a code-governed system post
// — the two flagship posts plus the advisor. Such rows carry 必须=是
// (auto-recruited / auto-created when the studio is created), are
// seeded or migrated by code, and refuse UI edits and deletes.
func SystemPostKey(key string) bool {
	if key == AdvisorPost.Key {
		return true
	}
	for _, p := range FlagshipPosts {
		if p.Key == key {
			return true
		}
	}
	return false
}

// EstablishmentDocKey is the scope's table address: the lobby keeps
// the v1 ops/establishment; a project's table is p/<key>/establishment.
// One rule for the keeper, the orchestrator's self-registration and
// the boot recruiter — the lobby must never grow a p/default shadow
// table nobody watches.
func EstablishmentDocKey(projectKey string) string {
	if projectKey == wire.LobbyKey {
		return "ops/establishment"
	}
	return "p/" + projectKey + "/establishment"
}

// ChronicleDocKey is the scope's 工作室志 address — the same per-office
// rule as EstablishmentDocKey: the lobby keeps the v1 ops/chronicle
// (the studio-wide journal the cockpit digests), a project's journal
// is p/<key>/chronicle so one office's blackboard never shows another
// office's ledger. Volume rollover rides the key (<key>-vol-NNN), so
// a project's volumes stay in its own namespace too. Empty projectKey
// reads as the lobby (the task engine's read-side default).
func ChronicleDocKey(projectKey string) string {
	if projectKey == "" || projectKey == wire.LobbyKey {
		return "ops/chronicle"
	}
	return "p/" + projectKey + "/chronicle"
}

// LiveEstablishmentRows parses one establishment table and reconciles
// presence from LIVE SEATS ONLY (exact role match against the room's
// roster) — the boot recruiter's strict view. The report endpoint's
// Establishment counts active configs as offline seats (the keeper's
// semantics: hired-but-offline IS staffed); the recruiter must not —
// its own pre-birth config upsert would otherwise satisfy its own
// gate and the door would open on a hire that never happened (no
// session, no seat, no ZCode sidebar row). ParseError is "" on
// success; rows are nil with it set.
func LiveEstablishmentRows(r Roster, docs *DocsStore, docKey string) ([]EstablishmentRow, string) {
	doc, err := docs.Get(docKey, 0)
	if err != nil {
		return nil, i18n.Sf("编制表文档不可读：%v（用 kb write %s 重建，表头需为冻结六列）", err, docKey)
	}
	rows, perr := EstablishmentRows(doc.Body)
	if perr != "" {
		return nil, perr
	}
	present := map[string][]string{}
	for _, m := range r.Members() {
		if m.Role != "" {
			present[m.Role] = append(present[m.Role], m.Name)
		}
	}
	for i := range rows {
		reconcileRow(&rows[i], present)
	}
	return rows, ""
}

// FlagshipShortage picks the flagship posts a reconciled establishment
// report leaves unfilled (present < headcount by the report's own
// exact-role match): the boot recruiter's birth list, in table order.
func FlagshipShortage(rows []EstablishmentRow) []FlagshipPost {
	byKey := make(map[string]EstablishmentRow, len(rows))
	for _, r := range rows {
		byKey[r.Key] = r
	}
	var missing []FlagshipPost
	for _, p := range FlagshipPosts {
		r, ok := byKey[p.Key]
		if !ok || len(r.Present) < p.Headcount {
			missing = append(missing, p)
		}
	}
	return missing
}

// FlagshipFilled counts seated flagship headcount over a reconciled
// report: present roles clipped at each post's headcount — the splash's
// "已到岗 n/N" numerator; total is always the sum of headcounts.
func FlagshipFilled(rows []EstablishmentRow) (filled, total int) {
	byRole := map[string]int{}
	for _, r := range rows {
		byRole[r.Role] = len(r.Present)
	}
	for _, p := range FlagshipPosts {
		total += p.Headcount
		got := byRole[p.Role]
		if got > p.Headcount {
			got = p.Headcount
		}
		filled += got
	}
	return filled, total
}

// RetireSentinelPosts removes the retired sentinel post row from every
// establishment table in the store — the one-time migration that
// converges existing rooms onto the post-flagship establishment (the
// v0.10 keeper already owns the watch; a lingering sentinel row would
// alarm forever about a post nobody may hold again). A broken table is
// refused, never edited on a guess (§12-5); the row's removal rides
// WriteExpect's optimistic lock with the same bounded retry as
// EnsureEstablishmentPost. Returns the doc keys actually rewritten.
func RetireSentinelPosts(docs *DocsStore) []string {
	if docs == nil {
		return nil
	}
	var touched []string
	for _, meta := range docs.List() {
		if !isEstablishmentDocKey(meta.Key) {
			continue
		}
		if ok, _ := retireSentinelRow(docs, meta.Key); ok {
			touched = append(touched, meta.Key)
		}
	}
	return touched
}

// isEstablishmentDocKey matches the lobby table plus every project's.
func isEstablishmentDocKey(key string) bool {
	return key == "ops/establishment" ||
		(strings.HasPrefix(key, "p/") && strings.HasSuffix(key, "/establishment"))
}

// retireSentinelRow drops the sentinel row from one table. ok=false
// covers nothing-to-do and every refusal — the caller only reports
// rewrites.
func retireSentinelRow(docs *DocsStore, docKey string) (bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		doc, err := docs.Get(docKey, 0)
		if err != nil {
			return false, err
		}
		rows, perr := EstablishmentRows(doc.Body)
		if perr != "" {
			return false, fmt.Errorf("编制表 %s 损坏，拒绝退役哨兵行（%s）", docKey, perr)
		}
		has := false
		for _, r := range rows {
			if r.Key == "sentinel" {
				has = true
				break
			}
		}
		if !has {
			return false, nil // already retired: idempotent
		}
		body, ok := dropRowByKey(doc.Body, "sentinel")
		if !ok {
			return false, fmt.Errorf("编制表 %s 未找到冻结表头行，拒绝退役哨兵行", docKey)
		}
		if _, werr := docs.WriteExpect(docKey, doc.Title, body, "看护", doc.Rev); werr == nil {
			return true, nil
		} else if !isRevConflict(werr) {
			return false, werr
		}
	}
	return false, fmt.Errorf("编制表 %s 哨兵退役与房主编辑冲突，重试耗尽——下次启动会再试", docKey)
}

// dropRowByKey removes every post-header table line whose 岗位 key
// cell equals key. The caller must have parsed the body successfully
// already; ok=false means no header was found — refuse rather than
// guess.
func dropRowByKey(body, key string) (string, bool) {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	headerSeen := false
	dropped := false
	for _, line := range lines {
		if !strings.Contains(line, "|") {
			out = append(out, line)
			continue
		}
		cells := splitTableRow(line)
		if !headerSeen {
			if len(cells) >= len(establishmentColumns) && isHeaderRow(cells[:len(establishmentColumns)]) {
				headerSeen = true
			}
			out = append(out, line)
			continue
		}
		if len(cells) >= len(establishmentColumns) && strings.TrimSpace(cells[0]) == key {
			dropped = true
			continue
		}
		out = append(out, line)
	}
	if !headerSeen {
		return "", false
	}
	if !dropped {
		return body, true
	}
	return strings.Join(out, "\n"), true
}
