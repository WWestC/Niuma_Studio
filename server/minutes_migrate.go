package server

// The v2.9 isolation backfill for meeting minutes: review minutes
// used to land on the hall's ops/meetings/ shelf regardless of which
// room held the meeting — a project's business sitting on the
// studio-operations shelf, invisible to the project and editable by
// nobody under the new room-scoped write gate. New minutes follow
// the room (dispatch's writeMinutes keys p/<key>/meetings/); this
// one-shot sweep refiles the historic ones by resolving each doc's
// requirement (the key's leaf is the lowercased r_NN id — "r_25" →
// "r25-3") through the requirements store to its project. Minutes
// with no resolvable requirement (free-form meetings, deleted reqs)
// honestly stay on the hall's shelf — their room can no longer be
// attributed, and the hall is where pre-isolation minutes lived.

import (
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/requirements"
)

// minutesLeafRE matches the legacy key leaf: "<lowercased r_NN>-<seq>"
// (free-form meetings write "free-<seq>" and never match — they are
// the unattributable residue by design).
var minutesLeafRE = regexp.MustCompile(`^r(\d+)-\d+$`)

// RefileProjectMinutes moves every ops/meetings/<rNN>-<seq> doc whose
// requirement belongs to a non-lobby project onto that project's
// p/<key>/meetings/ shelf (active and archived alike). Idempotent: a
// target already in place skips the move, so re-runs converge. The
// return lists the moves for the boot log.
func RefileProjectMinutes(docs *kb.DocsStore, reqs requirements.Store, subject string) []string {
	if docs == nil || reqs == nil {
		return nil
	}
	var moved []string
	for _, archived := range []bool{false, true} {
		for _, meta := range docs.ListOpt(kb.ListOptions{Archived: archived}) {
			if !strings.HasPrefix(meta.Key, kb.LobbyShelfPrefix+"meetings/") {
				continue
			}
			leaf := strings.TrimPrefix(meta.Key, kb.LobbyShelfPrefix+"meetings/")
			m := minutesLeafRE.FindStringSubmatch(leaf)
			if m == nil {
				continue
			}
			// 归属解析走跨架检索（v2.10 号段分家后裸号不再全局唯一）：
			// 唯一命中才搬——同号两架＝归属不可考，如实留大厅。
			hits := reqs.FindAll(subject, "r_"+m[1])
			if len(hits) != 1 {
				continue
			}
			r := hits[0]
			if r.ProjectKey == "" || r.ProjectKey == chat.LobbyKey {
				continue
			}
			n, err := strconv.Atoi(m[1])
			if err != nil || n <= 0 {
				continue
			}
			dst := kb.ProjectDocKey(r.ProjectKey, "meetings/"+leaf)
			if _, err := docs.Get(dst, 0); err == nil {
				continue // already refiled (or a same-named collision): leave the pair alone
			}
			doc, err := docs.Get(meta.Key, 0)
			if err != nil {
				continue
			}
			if _, err := docs.Write(dst, doc.Title, doc.Body, "隔离迁移"); err != nil {
				log.Printf("minutes: 迁移 %s → %s 失败（保留原位）：%v", meta.Key, dst, err)
				continue
			}
			if err := docs.Delete(meta.Key, "隔离迁移"); err != nil {
				log.Printf("minutes: 迁移 %s → %s 后原键删除失败（两键并存，下次启动重试会跳过）：%v", meta.Key, dst, err)
				continue
			}
			moved = append(moved, meta.Key+" → "+dst)
		}
	}
	return moved
}
