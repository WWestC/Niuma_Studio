package wire

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWireImportsNothingInternal is the layering keystone: the wire
// package is the protocol contract, importable standalone by an
// external client or a future platform front end. The day someone
// "just this once" imports a domain package here, the protocol starts
// rotting back into domain-shape coupling — this test is the tripwire.
func TestWireImportsNothingInternal(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue // tests MAY import domains (the compat test does, on purpose)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, "github.com/WWestC/Niuma_Studio") {
				t.Fatalf("%s imports %q — the wire package must stay self-contained; "+
					"domain payloads ride as mirror types owned here (see wire.go)", name, path)
			}
		}
	}
}

// TestMirrorFilesStaySiblings pins the mirror discipline's home: the
// conversions are GENERATED into each domain package as
// wireconv_gen.go (tools/wiregen, from both struct shapes), and the
// hand-written remainder (non-mechanical helpers like WireTasksOf)
// lives beside it as wireconv.go wherever such a face exists. A
// reviewer diffing a domain change still sees the conversion change
// beside it — as a one-command regeneration. (Cheap presence check;
// the real teeth are the compat tests + the freshness test.)
func TestMirrorFilesStaySiblings(t *testing.T) {
	for _, dom := range []string{"tasks", "plan", "merge", "requirements", "meeting"} {
		if _, err := os.Stat(filepath.Join("..", dom, "wireconv_gen.go")); err != nil {
			t.Fatalf("%s/wireconv_gen.go missing: %v", dom, err)
		}
	}
	// The one hand-written survivor: tasks' read-side sugar.
	if _, err := os.Stat(filepath.Join("..", "tasks", "wireconv.go")); err != nil {
		t.Fatalf("tasks/wireconv.go missing (the hand-written helper face): %v", err)
	}
}
