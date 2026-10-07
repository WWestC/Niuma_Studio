package chat

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestChatImportsNoDomain is the room-runtime half of the wire split:
// chat (hub, history, ledgers) may depend only on leaf packages — the
// protocol contract (wire) and infrastructure (util/i18n/projects/
// media/persist). Before the wire package existed, chat/wire.go pulled
// tasks/plan/merge/meeting/requirements in, and every domain change
// silently threatened the wire; that door stays closed now.
//
// The allowlist is deliberate: the original blacklist (banned twenty
// package names) could not recognize a NEW domain package — the next
// kb-shaped breach would simply not be on the list. Under a whitelist
// any new dependency is a visible, reviewable layering decision.
func TestChatImportsNoDomain(t *testing.T) {
	allowed := map[string]bool{
		"/i18n":     true,
		"/media":    true,
		"/persist":  true,
		"/projects": true,
		"/util":     true,
		"/wire":     true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue // tests MAY import domains (fixtures do, on purpose)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/WWestC/Niuma_Studio") {
				continue // stdlib is none of this test's business
			}
			if !allowed[strings.TrimPrefix(path, "github.com/WWestC/Niuma_Studio")] {
				t.Fatalf("%s imports %q — chat is the room runtime, not a domain "+
					"orchestrator; payloads cross this boundary as wire mirror "+
					"types only (see wire/wire.go). 新依赖＝分层决策：先在此白名单"+
					"加行并成文，而不是绕过测试", name, path)
			}
		}
	}
}
