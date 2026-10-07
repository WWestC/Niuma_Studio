// The desktop_harness → niuma rename moved every on-disk home path
// (the v2 store root, the flat ledgers, the seat tokens, the wait
// cursors). One startup, one rename each: an old name moves onto the
// new name when the new one is absent; when both exist the NEW one
// wins and the old file stays untouched for forensics. Best-effort by
// design — a failed rename logs and that store degrades exactly like
// an unreadable file: the room must boot.
package util

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// legacyHomePairs are the exact-name moves (root dir + flat files).
var legacyHomePairs = []struct{ old, new string }{
	{".desktop_harness", ".niuma"}, // the v2 root: history/room/staffing/capabilities/plan/…
	{".desktop_harness_tasks.json", ".niuma_tasks.json"},
	{".desktop_harness_ranks.json", ".niuma_ranks.json"},
	{".desktop_harness_agents.json", ".niuma_agents.json"},
	{".desktop_harness_blackboard.md", ".niuma_blackboard.md"},
	{".desktop_harness_kb", ".niuma_kb"},
	{".desktop_harness_port", ".niuma_port"},
}

// legacyHomePrefixes are the per-name runtime files (seat tokens, wait
// cursors): <old-prefix><rest> moves onto <new-prefix><rest>.
var legacyHomePrefixes = []struct{ old, new string }{
	{".desktop_harness_token_", ".niuma_token_"},
	{".desktop_harness_wait_", ".niuma_wait_"},
}

// MigrateLegacyPaths moves the desktop_harness-era home files onto
// their niuma names. Called once at process entry (app mode and CLI
// subcommands alike) before any store opens.
func MigrateLegacyPaths() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, p := range legacyHomePairs {
		renameHomePath(filepath.Join(home, p.old), filepath.Join(home, p.new))
	}
	for _, p := range legacyHomePrefixes {
		matches, err := filepath.Glob(filepath.Join(home, p.old+"*"))
		if err != nil {
			continue
		}
		for _, old := range matches {
			base := filepath.Base(old)
			renameHomePath(old, filepath.Join(home, p.new+strings.TrimPrefix(base, p.old)))
		}
	}
}

// renameHomePath is the one old→new move: absent old = nothing to do
// (the common post-migration case), present new = old stays put.
func renameHomePath(old, new string) {
	if _, err := os.Lstat(old); err != nil {
		return
	}
	if _, err := os.Lstat(new); err == nil {
		return
	}
	if err := os.Rename(old, new); err != nil {
		log.Printf("migrate %s → %s: %v — 以新路径继续（旧文件保留）", old, new, err)
	}
}
