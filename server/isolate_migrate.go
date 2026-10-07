package server

// The number-range isolation's FULL renumber sweep (v2.11 号段完全重排,
// superseding v2.10's guarded one): every project — the LOBBY INCLUDED —
// renumbers its requirements, meetings AND TASKS compactly from
// r_01/m_01/t_01 in insertion order, and every studio-owned surface
// that cites the old numbers rides along: in-ledger references (the
// pending plan's req field and prose, task req/parent/deps/proposal
// deps/log prose, meeting ReqIDs, branch-link bindings, the pending
// merge proposal's req/tasks), minutes leaf keys, kb doc bodies
// (chronicle, continuity ledgers, any prose — grouped by the doc's
// owning project) and the chat history jsonls.
//
// The v2.10 exemptions are repealed by owner's ruling: task ids used
// to be spared because repo commit messages cite them immutably — but
// each project's repo is its own, so a stale number only dangles
// inside that project's own history, which is accepted. The LOBBY
// used to be spared for the same reason; it is just another room now.
// The v2.10 guard (idReferencedOutside: a number cited in a commit
// message or doc body kept its old id) is gone entirely — hard
// renumber, full rewrite of everything the studio can write.
//
// The only accepted danglers: git commit messages (immutable by
// nature) and terminal transcripts (append-only streams with no
// rewrite face). Doc keys whose leaf embeds a NON-canonical number
// shape (e.g. p/book/r38-writer-input) keep their key; only canonical
// r_NN/t_NN/m_NN tokens in bodies/text swap.
//
// Idempotent: a second run finds compact shelves and empty plans; doc
// and chat rewrites no-hit and leave files untouched.

import (
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// renumberLeafRE matches the project-minutes leaf "<lowercased id>-<seq>"
// (same shape as minutes_migrate's, reused for the leaf rekey).
var renumberLeafRE = regexp.MustCompile(`^(r|m)(\d+)-\d+$`)

// RenumberIsolatedLedgers runs the one-shot sweep (boot order: after
// RefileProjectMinutes and the stores are open, BEFORE any chat hub
// owns a history file). histDir is the registry's history root ("" =
// no persisted chat to rewrite). Returns one report line per project
// for the boot log; nil = nothing to do.
func RenumberIsolatedLedgers(
	docs *kb.DocsStore,
	reqs requirements.Store,
	meets meeting.Store,
	engine *tasks.Engine,
	projStore *projects.Store,
	plans plan.Store,
	merges merge.Store,
	histDir string,
	subject string,
) []string {
	if reqs == nil || projStore == nil {
		return nil
	}
	// Per-project merged mapping (r+m+t) for the doc-body and chat
	// rewrites — kept per project so two shelves' same number (book's
	// r_37 vs the lobby's r_37) can never swap each other's citations.
	byProject := map[string]map[string]string{}
	var report []string
	for _, p := range projStore.List() {
		mapping, line := renumberProject(docs, reqs, meets, engine, projStore, plans, merges, p, subject)
		if len(mapping) > 0 {
			byProject[p.Key] = mapping
		}
		if line != "" {
			report = append(report, p.Key+": "+line)
		}
	}
	if docs != nil {
		if n := remapDocBodies(docs, byProject); n > 0 {
			log.Printf("isolate: 知识库正文改写 %d 篇", n)
		}
	}
	if histDir != "" {
		for key, mapping := range byProject {
			if n := chat.RemapHistoryIDs(filepath.Join(histDir, key+".jsonl"), mapping); n > 0 {
				log.Printf("isolate: %s 聊天历史改写 %d 帧", key, n)
			}
		}
	}
	return report
}

func renumberProject(
	docs *kb.DocsStore,
	reqs requirements.Store,
	meets meeting.Store,
	engine *tasks.Engine,
	projStore *projects.Store,
	plans plan.Store,
	merges merge.Store,
	p projects.Project,
	subject string,
) (mapping map[string]string, line string) {
	var parts []string
	merged := map[string]string{}

	// --- requirements r_NN -------------------------------------------------
	reqPlan := reqs.RenumberPlan(subject, p.Key)
	if len(reqPlan) > 0 {
		if err := reqs.RenumberApply(subject, p.Key, reqPlan); err != nil {
			log.Printf("isolate: %s 需求重排失败（保留原号）：%v", p.Key, err)
		} else {
			parts = append(parts, describeMap("需求", reqPlan))
			for k, v := range reqPlan {
				merged[k] = v
			}
			// in-ledger references ride along
			if plans != nil {
				plans.RemapReq(subject, p.Key, reqPlan)
			}
			if engine != nil {
				engine.RemapReq(p.Key, reqPlan)
			}
			if meets != nil {
				meets.RemapReq(p.Key, reqPlan)
			}
			if merges != nil {
				merges.RemapIDs(subject, p.Key, reqPlan, nil)
			}
			remapBranchLinks(projStore, p, reqPlan)
			rekeyMinutesLeaves(docs, p.Key, reqPlan)
		}
	}

	// --- meetings m_NN -----------------------------------------------------
	if meets != nil {
		meetPlan := meets.RenumberPlan(p.Key)
		if len(meetPlan) > 0 {
			if err := meets.RenumberApply(p.Key, meetPlan); err != nil {
				log.Printf("isolate: %s 会议重排失败（保留原号）：%v", p.Key, err)
			} else {
				parts = append(parts, describeMap("会议", meetPlan))
				for k, v := range meetPlan {
					merged[k] = v
				}
			}
		}
	}

	// --- tasks t_NN (v2.11: the exemption is repealed) ----------------------
	if engine != nil {
		taskPlan := engine.RenumberPlan(p.Key)
		if len(taskPlan) > 0 {
			if err := engine.RenumberApply(p.Key, taskPlan); err != nil {
				log.Printf("isolate: %s 任务重排失败（保留原号）：%v", p.Key, err)
			} else {
				parts = append(parts, describeMap("任务", taskPlan))
				for k, v := range taskPlan {
					merged[k] = v
				}
				if plans != nil {
					plans.RemapTasks(subject, p.Key, taskPlan)
				}
				if merges != nil {
					merges.RemapIDs(subject, p.Key, nil, taskPlan)
				}
			}
		}
	}

	return merged, strings.Join(parts, "；")
}

// docOwner reads a doc key's owning project: p/<key>/<leaf> belongs to
// <key>, everything else (ops/…, bare keys) reads as the lobby's —
// the v2.10 doc-shelving convention. Unknown project keys fall back
// to the lobby (their shelf contributed no mapping, so it's a no-op).
func docOwner(key string) string {
	rest, ok := strings.CutPrefix(key, "p/")
	if !ok {
		return projects.LobbyKey
	}
	if i := strings.Index(rest, "/"); i > 0 {
		return rest[:i]
	}
	return projects.LobbyKey
}

// remapDocBodies swaps every canonical r_NN/t_NN/m_NN token in every
// doc's title and body per the owning project's mapping (both archive
// states; minutes shelves were already rekeyed — their BODIES ride
// here too, the guard era treated them as read-only prose). A doc
// where nothing hits is not rewritten.
func remapDocBodies(docs *kb.DocsStore, byProject map[string]map[string]string) int {
	if len(byProject) == 0 {
		return 0
	}
	n := 0
	for _, archived := range []bool{false, true} {
		for _, meta := range docs.ListOpt(kb.ListOptions{Archived: archived}) {
			mapping := byProject[docOwner(meta.Key)]
			if len(mapping) == 0 {
				continue
			}
			doc, err := docs.Get(meta.Key, 0)
			if err != nil {
				continue
			}
			title := chat.SwapIDTokens(doc.Title, mapping)
			body := chat.SwapIDTokens(doc.Body, mapping)
			if title == doc.Title && body == doc.Body {
				continue
			}
			if _, err := docs.Write(meta.Key, title, body, "号段重排"); err != nil {
				log.Printf("isolate: 文档 %s 正文改写失败（保留原文）：%v", meta.Key, err)
				continue
			}
			n++
		}
	}
	return n
}

// remapBranchLinks rewrites the project's branch bindings whose Req
// rode in mapping (BindBranch is the wholesale-replace write face).
func remapBranchLinks(projStore *projects.Store, p projects.Project, mapping map[string]string) {
	if projStore == nil || len(p.BranchLinks) == 0 {
		return
	}
	for branch, bl := range p.BranchLinks {
		nn, ok := mapping[bl.Req]
		if !ok {
			continue
		}
		bl.Req = nn
		if _, err := projStore.BindBranch(p.Key, branch, bl); err != nil {
			log.Printf("isolate: %s 分支 %s 的需求绑定改写失败（保留原号）：%v", p.Key, branch, err)
		}
	}
}

// rekeyMinutesLeaves moves p/<key>/meetings/<id>-<seq> docs whose
// requirement id rode in mapping to the new leaf — write-then-delete,
// target-exists skip, the minutes_migrate discipline verbatim.
func rekeyMinutesLeaves(docs *kb.DocsStore, key string, mapping map[string]string) {
	if docs == nil || len(mapping) == 0 {
		return
	}
	prefix := kb.ProjectDocKey(key, "meetings/")
	for _, archived := range []bool{false, true} {
		for _, meta := range docs.ListOpt(kb.ListOptions{Archived: archived}) {
			if !strings.HasPrefix(meta.Key, prefix) {
				continue
			}
			leaf := strings.TrimPrefix(meta.Key, prefix)
			m := renumberLeafRE.FindStringSubmatch(leaf)
			if m == nil {
				continue
			}
			old := m[1] + "_" + m[2]
			nn, ok := mapping[old]
			if !ok {
				continue
			}
			newLeaf := strings.Replace(leaf, m[1]+m[2], m[1]+strings.TrimPrefix(nn, m[1]+"_"), 1)
			dst := kb.ProjectDocKey(key, "meetings/"+newLeaf)
			if _, err := docs.Get(dst, 0); err == nil {
				continue
			}
			doc, err := docs.Get(meta.Key, 0)
			if err != nil {
				continue
			}
			if _, err := docs.Write(dst, doc.Title, doc.Body, "号段重排"); err != nil {
				log.Printf("isolate: 纪要 %s → %s 失败（保留原键）：%v", meta.Key, dst, err)
				continue
			}
			if err := docs.Delete(meta.Key, "号段重排"); err != nil {
				log.Printf("isolate: 纪要 %s 迁移后原键删除失败（两键并存，重跑会跳过）：%v", meta.Key, err)
			}
		}
	}
}

func describeMap(kind string, mapping map[string]string) string {
	var moves []string
	for old, nn := range mapping {
		moves = append(moves, old+"→"+nn)
	}
	sortStrings(moves)
	return kind + " " + strings.Join(moves, "、")
}

func sortStrings(list []string) {
	sort.Strings(list)
}
