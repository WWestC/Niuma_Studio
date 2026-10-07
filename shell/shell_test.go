package shell

// shell_test.go — bundleOfficeHome's climb and its go.mod gate: the
// bundle ships from the office checkout, so the home is four Dir()
// hops up — and ONLY when a go.mod vouches for the directory (a
// relocated bundle in /Applications must not offer itself as the
// members' workspace).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleOfficeHomeClimb(t *testing.T) {
	office := t.TempDir()
	if err := os.WriteFile(filepath.Join(office, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	exe := filepath.Join(office, "Niuma_Studio.app", "Contents", "MacOS", "niuma")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatalf("bundle tree: %v", err)
	}
	if got := bundleOfficeHome(exe); got != office {
		t.Fatalf("climb: got %q want %q", got, office)
	}
}

func TestBundleOfficeHomeGates(t *testing.T) {
	// No go.mod beside the .app (a relocated bundle): no home offered.
	plain := t.TempDir()
	exe := filepath.Join(plain, "Niuma_Studio.app", "Contents", "MacOS", "niuma")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatalf("bundle tree: %v", err)
	}
	if got := bundleOfficeHome(exe); got != "" {
		t.Fatalf("relocated bundle offered a home: %q", got)
	}
	// A plain (non-bundle) binary path: never a home.
	if got := bundleOfficeHome(filepath.Join(plain, "bin", "niuma")); got != "" {
		t.Fatalf("non-bundle path offered a home: %q", got)
	}
}
