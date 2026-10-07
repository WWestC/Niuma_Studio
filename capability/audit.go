// Assembly audit: every accepted assemble op appends one row to
// ~/.niuma/audit/assembly.jsonl — who, when, which seat, what moved,
// the effective-fabric digest and the surface it rode in on.
// Append-only, never rewritten: the same log-as-truth discipline as
// history/inbox. Rows written before the pack→skill/MCP rename keep
// their old packs/prompt_bytes fields — history is not rewritten.
package capability

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/WWestC/Niuma_Studio/util"
)

// Assembly vocabulary: the target an op lands on and the list op
// itself. Shared by the wire faces and the audit rows.
const (
	TargetStaffing = "staffing" // the seat-level override
	TargetProfile  = "profile"  // the profile default

	OpSet    = "set"    // replace the whole list
	OpAdd    = "add"    // append the missing, order-preserving
	OpRemove = "remove" // subtract the named keys
)

// AssemblyEvent is one audit row: one accepted assemble op. Skills
// and MCPServer are the RESULTING lists (what the seat now runs), not
// the deltas.
type AssemblyEvent struct {
	TS         int64         `json:"ts"`
	Actor      string        `json:"actor"`
	ProjectKey string        `json:"project_key,omitempty"` // empty for profile targets
	Person     string        `json:"person"`
	Target     string        `json:"target"`    // staffing|profile
	Op         string        `json:"op"`        // set|add|remove
	Skills     []string      `json:"skills"`    // the resulting skill list
	MCPServers []string      `json:"mcps"`      // the resulting MCP server list
	Effective  *FabricDigest `json:"effective"` // what the seat now runs on
	Via        string        `json:"via"`       // cli|http|ws|chat
}

// FabricDigest summarizes an effective fabric for the audit row: the
// governance-relevant slots plus the fabric's byte weight. The skill
// bodies never enter the audit.
type FabricDigest struct {
	Model     string   `json:"model,omitempty"` // "id" or "id/reasoning"
	MCP       []string `json:"mcp,omitempty"`   // assembled server keys
	BodyBytes int      `json:"body_bytes"`      // composed fabric byte weight
}

// Digest folds the fabric into its audit summary.
func (f EffectiveFabric) Digest() FabricDigest {
	d := FabricDigest{BodyBytes: len(f.Text)}
	if f.Model != nil {
		d.Model = f.Model.ID
		if f.Model.Reasoning != "" {
			d.Model += "/" + f.Model.Reasoning
		}
	}
	for _, m := range f.MCP {
		d.MCP = append(d.MCP, m.Key)
	}
	return d
}

// AuditLog appends assembly rows to one jsonl file. An empty path (or
// a nil log) discards silently — a missing audit must never refuse an
// assembly; write failures log instead.
type AuditLog struct {
	mu   sync.Mutex
	path string
}

// DefaultAuditPath returns the conventional v2 location:
// ~/.niuma/audit/assembly.jsonl.
func DefaultAuditPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma", "audit", "assembly.jsonl"), nil
}

// OpenAudit returns the appender for path ("" = discard).
func OpenAudit(path string) *AuditLog {
	return &AuditLog{path: path}
}

// Append writes one row, stamping ts when the caller did not.
func (a *AuditLog) Append(ev AssemblyEvent) {
	if a == nil || a.path == "" {
		return
	}
	if ev.TS == 0 {
		ev.TS = util.Now()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		log.Printf("audit assembly: %v", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		log.Printf("audit assembly: %v", err)
		return
	}
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("audit assembly: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		log.Printf("audit assembly: %v", err)
	}
}
