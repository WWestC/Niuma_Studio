package projects

import (
	"path/filepath"
	"testing"
)

// The lobby's frozen workspace yields to root poison and nothing else:
// a GUI launch freezes "/" (the process cwd of every Finder/Dock
// start), the next boot rewrites it to the caller's resolved workspace
// — and the rewrite lands on disk. Every legitimate freeze stays
// immutable, and a degraded caller that can only offer the root again
// must not bounce a healed lobby back into poison.
func TestEnsureLobbyRootPoisonRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	good := filepath.Join(t.TempDir(), "office")
	if _, err := store.EnsureLobby(good); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	// Simulate the poisoned freeze: the store was first stamped by a
	// launch whose cwd was the filesystem root.
	store.list[0].Workspace = "/"

	if p, err := store.EnsureLobby(good); err != nil {
		t.Fatalf("repair boot: %v", err)
	} else if p.Workspace != good {
		t.Fatalf("root poison not repaired: workspace=%q want %q", p.Workspace, good)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if p, ok := reopened.Get(LobbyKey); !ok || p.Workspace != good {
		t.Fatalf("repair not persisted: ok=%v workspace=%q want %q", ok, p.Workspace, good)
	}

	// A legitimate freeze never moves for a different caller value.
	other := filepath.Join(t.TempDir(), "elsewhere")
	if _, err := reopened.EnsureLobby(other); err != nil {
		t.Fatalf("later boot: %v", err)
	}
	if p, _ := reopened.Get(LobbyKey); p.Workspace != good {
		t.Fatalf("legitimate freeze rewritten: workspace=%q want %q", p.Workspace, good)
	}
	// The degraded machine (no home dir — the only caller that still
	// offers "/") keeps its lobby; it must not re-poison a healed one.
	if _, err := reopened.EnsureLobby("/"); err != nil {
		t.Fatalf("degraded boot: %v", err)
	}
	if p, _ := reopened.Get(LobbyKey); p.Workspace != good {
		t.Fatalf("healed lobby re-poisoned: workspace=%q want %q", p.Workspace, good)
	}
}

// The lobby's display name: fresh boots are born Niuma_Studio, a
// legacy「大厅」entry upgrades in place (and persists), and a renamed
// lobby is a deliberate user pick that no boot may churn.
func TestEnsureLobbyDisplayName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	office := filepath.Join(t.TempDir(), "office")
	if p, err := store.EnsureLobby(office); err != nil {
		t.Fatalf("first boot: %v", err)
	} else if p.Name != LobbyName {
		t.Fatalf("fresh lobby name=%q want %q", p.Name, LobbyName)
	}
	// Idempotent: the second boot over the new name is a no-op.
	if _, err := store.EnsureLobby(office); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if p, _ := store.Get(LobbyKey); p.Name != LobbyName {
		t.Fatalf("name churned on re-boot: %q", p.Name)
	}

	// The v1 store: a lobby still titled「大厅」upgrades and persists.
	legacy := filepath.Join(t.TempDir(), "legacy.json")
	lstore, err := Open(legacy)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := lstore.EnsureLobby(office); err != nil {
		t.Fatalf("legacy first boot: %v", err)
	}
	lstore.list[0].Name = legacyLobbyName
	if p, err := lstore.EnsureLobby(office); err != nil {
		t.Fatalf("upgrade boot: %v", err)
	} else if p.Name != LobbyName {
		t.Fatalf("legacy name not upgraded: %q want %q", p.Name, LobbyName)
	}
	reopened, err := Open(legacy)
	if err != nil {
		t.Fatalf("reopen legacy: %v", err)
	}
	if p, ok := reopened.Get(LobbyKey); !ok || p.Name != LobbyName {
		t.Fatalf("upgrade not persisted: ok=%v name=%q", ok, p.Name)
	}

	// A hand-renamed lobby is the user's pick — no boot touches it.
	reopened.list[0].Name = "我的门厅"
	if p, err := reopened.EnsureLobby(office); err != nil {
		t.Fatalf("renamed boot: %v", err)
	} else if p.Name != "我的门厅" {
		t.Fatalf("user rename churned: %q", p.Name)
	}
}

// The degraded residue repairs like root poison: a lobby frozen at
// stableWorkspace's own ~/.niuma/workspace fallback is always that
// fallback's artifact — a bundle launch that can name the real office
// home (BundleOfficeHome) upgrades it, and the upgrade persists; the
// fallback itself must not churn a lobby already sitting on it.
func TestEnsureLobbyDegradedResidueRepair(t *testing.T) {
	degraded := degradedWorkspace()
	if degraded == "" || !filepath.IsAbs(degraded) {
		t.Fatalf("degraded workspace unresolvable: %q", degraded)
	}
	path := filepath.Join(t.TempDir(), "projects.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.EnsureLobby(degraded); err != nil {
		t.Fatalf("degraded first boot: %v", err)
	}
	office := filepath.Join(t.TempDir(), "office")
	if p, err := store.EnsureLobby(office); err != nil {
		t.Fatalf("repair boot: %v", err)
	} else if p.Workspace != office {
		t.Fatalf("degraded residue not repaired: workspace=%q want %q", p.Workspace, office)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if p, ok := reopened.Get(LobbyKey); !ok || p.Workspace != office {
		t.Fatalf("residue repair not persisted: ok=%v workspace=%q want %q", ok, p.Workspace, office)
	}
	// The caller that can still only offer the fallback leaves it be —
	// no churn, and no downgrade of the healed value either.
	if _, err := reopened.EnsureLobby(degraded); err != nil {
		t.Fatalf("fallback boot: %v", err)
	}
	if p, _ := reopened.Get(LobbyKey); p.Workspace != office {
		t.Fatalf("healed lobby downgraded to fallback: workspace=%q want %q", p.Workspace, office)
	}
}
