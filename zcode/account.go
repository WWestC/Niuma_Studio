package zcode

// account.go — the ACCOUNT-plan model providers, the missing half of
// the pick list. The desktop's model picker offers three kinds of
// groups: the personal providers from ~/.zcode/v2/provider_config.json
// (ListPersonalProviders, the v1 pick list), AND the built-in
// account-plan providers — "BigModel 团队" (account:bigmodel-team-
// coding-plan), "Start Plan 免费" (account:bigmodel-start-plan), and
// their zai/individual/off-peak siblings. Those live in the CLI's
// bundled zcode-builtin.json (providerRules with access.type
// "zhipu-account", each carrying its builtinModelIds) — the very file
// providerEnv already locates to hand the app-server child, so the
// list reads the same source of truth the runtime drives.
//
// Three desktop behaviors this composition reproduces with LOCAL data
// only (the desktop checks live plan entitlement remotely, which a
// plain file reader must approximate):
//   - which faces exist: every visible face in the builtin config
//     stands, keyless or not — the desktop's registry keeps un-drivable
//     faces and marks them unselectable, it never drops them (only
//     visibility:hidden ships hidden; the off-peak faces ride that).
//     Niuma's drivability verdict rides along as data: config.json
//     (~/.zcode/v2/config.json) holds a plaintext apiKey per builtin:*
//     provider, and the paid coding-plan faces (team/individual)
//     additionally follow the family's selected connection in
//     setting.json (providerFamilyConnectionSelections);
//   - which models a face shows: Start Plan's rule ships three ids
//     but the free tier's per-period grant is smaller (the desktop
//     trims against the plan service's balances) — the trim is pinned
//     here, applied only to the available verdict (an un-drivable face
//     keeps the full builtin list, as the desktop's unentitled push
//     leaves the shipped models untouched);
//   - which faces can actually be driven: only the available ones get
//     pushed entitled into the child's registry (SyncAccountProviders)
//     — the pick list may show the rest, the runtime must not serve
//     them.
//
// Driving an account model needs one more wire piece: every turn
// makes the app-server ask its host
// interaction/requestProviderRuntimeHeaders (AccountAPIKey answers
// it) — account providers carry no static key in the child's config.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// accountRule is one zcode-builtin.json providerRules row with a
// zhipu-account access (only the fields the composition needs).
type accountRule struct {
	ProviderID   string
	ProviderName string
	Mode         string // start-plan / individual-coding-plan / team-coding-plan / off-peak
	Family       string // access.accountType: zai / bigmodel
	Models       []string
	Visibility   string // "hidden" faces never enter the desktop picker either
}

// planBadge mirrors the desktop picker's connection-mode chips.
var planBadge = map[string]string{
	"team-coding-plan":       "团队",
	"individual-coding-plan": "个人",
	"start-plan":             "免费",
}

// startPlanEntitled pins the free tier's model trim (see the file
// comment): the builtin rule ships three ids, the plan serves one.
var startPlanEntitled = []string{"GLM-5.3-Flash"}

// parseAccountRules is the pure half of the builtin-config read
// (BOM-tolerant, garbage = no rules): only zhipu-account rules, only
// the fields the composition needs.
func parseAccountRules(b []byte) []accountRule {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var cfg struct {
		Config struct {
			ProviderConfigRules struct {
				ProviderRules []struct {
					ProviderID   string `json:"providerId"`
					ProviderName string `json:"providerName"`
					Config       struct {
						BuiltinModelIds []string `json:"builtinModelIds"`
						Visibility      string   `json:"visibility"`
						Access          struct {
							Type        string `json:"type"`
							Mode        string `json:"mode"`
							AccountType string `json:"accountType"`
						} `json:"access"`
					} `json:"config"`
				} `json:"providerRules"`
			} `json:"providerConfigRules"`
		} `json:"config"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	rules := cfg.Config.ProviderConfigRules.ProviderRules
	out := make([]accountRule, 0, len(rules))
	for _, r := range rules {
		if r.Config.Access.Type != "zhipu-account" {
			continue // api-key templates are not account faces
		}
		out = append(out, accountRule{
			ProviderID:   r.ProviderID,
			ProviderName: r.ProviderName,
			Mode:         r.Config.Access.Mode,
			Family:       r.Config.Access.AccountType,
			Models:       r.Config.BuiltinModelIds,
			Visibility:   r.Config.Visibility,
		})
	}
	return out
}

// selectedPlanKinds parses setting.json's
// providerFamilyConnectionSelections into family→kind ("bigmodel"→
// "team-coding-plan"). Garbled = no selections (paid faces then hide).
func selectedPlanKinds(b []byte) map[string]string {
	var s struct {
		ProviderFamilyConnectionSelections map[string]struct {
			Kind string `json:"kind"`
		} `json:"providerFamilyConnectionSelections"`
	}
	if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &s) != nil {
		return nil
	}
	out := make(map[string]string, len(s.ProviderFamilyConnectionSelections))
	for family, sel := range s.ProviderFamilyConnectionSelections {
		if sel.Kind != "" {
			out[family] = sel.Kind
		}
	}
	return out
}

// builtinAPIKeys parses config.json's provider registry into
// "builtin:…"-key → plaintext apiKey. Garbled = no keys (account
// faces then hide); encrypted desktop secrets never appear here —
// config.json stores these keys in the clear.
func builtinAPIKeys(b []byte) map[string]string {
	var c struct {
		Provider map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &c) != nil {
		return nil
	}
	out := make(map[string]string, len(c.Provider))
	for id, p := range c.Provider {
		if p.Options.APIKey != "" {
			out[id] = p.Options.APIKey
		}
	}
	return out
}

// builtinProviderFor maps an account provider id onto the config.json
// builtin:* entry that carries its apiKey — the CLI's own mapping
// (team AND individual land on the family coding-plan; start-plan has
// its own). "" = no mapping (off-peak and strangers). The family is
// whatever precedes the plan-mode suffix, so a future dash-bearing
// family name still splits right.
func builtinProviderFor(accountProviderID string) string {
	rest, ok := strings.CutPrefix(accountProviderID, "account:")
	if !ok {
		return ""
	}
	for suffix, builtin := range map[string]string{
		"-team-coding-plan":       "-coding-plan",
		"-individual-coding-plan": "-coding-plan",
		"-start-plan":             "-start-plan",
	} {
		if family, found := strings.CutSuffix(rest, suffix); found {
			return "builtin:" + family + builtin
		}
	}
	return ""
}

// composeAccountProviders is the pure composition: file order
// preserved, and every visible face STANDS — the desktop's registry
// keeps faces it can't currently drive and marks them unselectable
// (per-model disabledReason), it never drops them, so the local
// composition mirrors that and carries the drivability verdict as
// data instead of filtering. Off-peak faces ship visibility:hidden
// and never enter the desktop picker either. A face is available
// when Niuma holds its plaintext apiKey and — for the paid faces —
// the family's connection selection names it; Start Plan follows
// only the key (the desktop's own current-rule for the free tier).
// Start Plan trims to its pinned entitled model, the local stand-in
// for the service's per-period grant (balances capabilities) — and
// only when available: an unavailable face keeps the full builtin
// list exactly as the desktop's unentitled push leaves the shipped
// models in place.
func composeAccountProviders(rules []accountRule, kinds map[string]string, keys map[string]string) []ProviderModels {
	out := make([]ProviderModels, 0, len(rules))
	for _, r := range rules {
		if r.ProviderID == "" || r.Mode == "off-peak" || r.Visibility == "hidden" || len(r.Models) == 0 {
			continue
		}
		models := r.Models
		avail, reason := true, ""
		switch {
		case keys[builtinProviderFor(r.ProviderID)] == "":
			avail, reason = false, "not-connected"
		case r.Mode != "start-plan" && kinds[r.Family] != r.Mode:
			avail, reason = false, "connection-unselected"
		}
		if avail && r.Mode == "start-plan" {
			models = intersect(models, startPlanEntitled)
			if len(models) == 0 {
				models, avail, reason = r.Models, false, "no-entitled-models"
			}
		}
		out = append(out, ProviderModels{
			ProviderID:   r.ProviderID,
			ProviderName: r.ProviderName,
			Badge:        i18n.S(planBadge[r.Mode]),
			Models:       models,
			Available:    avail,
			Unavailable:  reason,
		})
	}
	return out
}

// intersect keeps a's order, only members of b.
func intersect(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if _, ok := set[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

// homeV2Path resolves ~/.zcode/v2/<rel>, collapsing a UserHomeDir
// failure to a relative path whose read fails — the callers degrade.
func homeV2Path(rel string) string {
	p, err := HomePathV2(rel)
	if err != nil {
		return filepath.Join(".zcode", "v2", rel)
	}
	return p
}

// splitAccountID breaks "account:<family>-<mode>" into its halves
// ("account:bigmodel-team-coding-plan" → bigmodel, team-coding-plan).
func splitAccountID(id string) (family, mode string, ok bool) {
	rest, isAccount := strings.CutPrefix(id, "account:")
	if !isAccount {
		return "", "", false
	}
	for suffix := range map[string]struct{}{
		"-individual-coding-plan": {},
		"-team-coding-plan":       {},
		"-start-plan":             {},
	} {
		if f, found := strings.CutSuffix(rest, suffix); found {
			return f, strings.TrimPrefix(suffix, "-"), true
		}
	}
	return "", "", false
}

// activeBuiltinPath is the ACTIVE builtin provider config — the newest
// runtime-refreshed copy under ~/.zcode/v2/runtime/provider/<os>-* /
// <ver>/endpoint-<key>/ (the very file the desktop runs its own
// children on and pushes account config against), falling back to the
// bundled copy. Every consumer of the builtin rules must read THIS
// one: the child's ZCODE_BUILTIN_PROVIDER_CONFIG_FILE (providerEnv),
// the account push's rules AND its basedOn stamp, and the pick list —
// a mixed hand (child on the bundled file, push keyed to the runtime
// copy) leaves the registry silently un-updated and every
// SetModel(account:…) rejected (房主实录 2026-10-07: double-click
// boots hung the warm-up 90s; desktop-descended envs worked).
func activeBuiltinPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	pattern := filepath.Join(home, ".zcode", "v2", "runtime", "provider",
		runtime.GOOS+"-*", "*", "endpoint-*", "zcode-builtin.json")
	if path, _ := latestBuiltinCopy(pattern); path != "" {
		return path
	}
	return findProviderConfig(FindBundle())
}

// builtinActiveRevision reproduces the child's own builtin-revision
// stamp — `zcode-builtin:<fileRevision>:<sha256(activePath)>` where
// the active file is the one activeBuiltinPath names (the
// account-config sync is refused as builtin-revision-mismatch when
// the stamp doesn't equal the child's own; the desktop pushes exactly
// this string).
func builtinActiveRevision() string {
	path := activeBuiltinPath()
	if path == "" {
		return ""
	}
	rev := builtinFileRevision(path)
	if rev == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("zcode-builtin:%s:%x", rev, sum)
}

// latestBuiltinCopy picks the most recently touched runtime copy.
func latestBuiltinCopy(pattern string) (path, rev string) {
	matches, _ := filepath.Glob(pattern)
	var newest string
	var newestAt time.Time
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.ModTime().After(newestAt) {
			newest, newestAt = m, fi.ModTime()
		}
	}
	if newest == "" {
		return "", ""
	}
	return newest, builtinFileRevision(newest)
}

// builtinFileRevision reads the release's integer revision ("30").
func builtinFileRevision(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var rel struct {
		Revision json.Number `json:"revision"`
	}
	if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &rel) != nil || rel.Revision == "" {
		return ""
	}
	return rel.Revision.String()
}

// buildAccountSyncPayload assembles the provider/updateAccountConfig
// push from the same local composition the pick list shows: every
// offered face becomes an entitled builtin-account entry (the child
// takes ONLY {access:{type,entitled}, builtinModelIds} — the api/base
// URLs come from its own builtin config), with the family's selected
// connection flagged current. The desktop is the shape reference: its
// own pushes carry exactly these fields plus per-face availability
// states, keyed to its computed builtin revision.
func buildAccountSyncPayload() (map[string]any, error) {
	builtin := activeBuiltinPath()
	if builtin == "" {
		return nil, errors.New("未找到 ZCode 内置 provider 配置")
	}
	bb, err := os.ReadFile(builtin)
	if err != nil {
		return nil, err
	}
	rules := parseAccountRules(bb)
	var kinds, keys map[string]string
	if sb, err := os.ReadFile(homeV2Path("setting.json")); err == nil {
		kinds = selectedPlanKinds(sb)
	}
	if cb, err := os.ReadFile(homeV2Path("config.json")); err == nil {
		keys = builtinAPIKeys(cb)
	}
	faces := composeAccountProviders(rules, kinds, keys)
	// only the drivable faces become entitled registry entries — the
	// composition now also carries the un-drivable ones (for the pick
	// list's disabled rows), and pushing those entitled would invite
	// turns no key can serve.
	drivable := make([]ProviderModels, 0, len(faces))
	for _, f := range faces {
		if f.Available {
			drivable = append(drivable, f)
		}
	}
	if len(drivable) == 0 {
		return nil, errors.New("本机没有任何可驱动的账号套餐面")
	}
	providers := make(map[string]any, len(drivable))
	states := make(map[string]any, len(drivable))
	for _, f := range drivable {
		providers[f.ProviderID] = map[string]any{
			"access":          map[string]any{"type": "zhipu-account", "entitled": true},
			"builtinModelIds": f.Models,
		}
		family, mode, ok := splitAccountID(f.ProviderID)
		current := ok && kinds[family] == mode
		states[f.ProviderID] = map[string]any{
			"availability": "available", "entitled": true, "current": current,
		}
	}
	basedOn := builtinActiveRevision()
	if basedOn == "" {
		return nil, errors.New("无法计算内置配置修订号")
	}
	body, _ := json.Marshal(faces)
	sum := sha256.Sum256(append([]byte(basedOn), body...))
	return map[string]any{
		"revision":                    fmt.Sprintf("niuma:%x", sum[:8]),
		"basedOnZCodeBuiltinRevision": basedOn,
		"providers":                   providers,
		"states":                      states,
	}, nil
}

// SyncAccountProviders pushes the account-plan faces into the
// app-server child's provider registry (provider/updateAccountConfig
// — the very channel the desktop host uses; a standalone child
// accepts it and refreshes its registry). Without the push the
// registry knows no account:* providers and every SetModel on one
// fails with 「Provider Registry 中不存在 Model」. Returns the
// child's receipt; the caller decides how loud a failure is.
func (c *Client) SyncAccountProviders(ctx context.Context) error {
	payload, err := buildAccountSyncPayload()
	if err != nil {
		return err
	}
	raw, err := c.callCtx(ctx, "provider/updateAccountConfig", payload)
	if err != nil {
		return err
	}
	var receipt struct {
		ReceivedRevision string `json:"receivedRevision"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.ReceivedRevision != payload["revision"] {
		return fmt.Errorf("account config 回执版本不一致: %s", raw)
	}
	return nil
}

// ListAccountProviders reads the three local files (the ACTIVE
// builtin config — see activeBuiltinPath; setting.json; config.json)
// and composes the account faces. Missing pieces degrade to a shorter
// list, never an error — the personal providers always stand.
func ListAccountProviders() []ProviderModels {
	builtin := activeBuiltinPath()
	if builtin == "" {
		return nil
	}
	bb, err := os.ReadFile(builtin)
	if err != nil {
		return nil
	}
	var kinds, keys map[string]string
	if sb, err := os.ReadFile(homeV2Path("setting.json")); err == nil {
		kinds = selectedPlanKinds(sb)
	}
	if cb, err := os.ReadFile(homeV2Path("config.json")); err == nil {
		keys = builtinAPIKeys(cb)
	}
	return composeAccountProviders(parseAccountRules(bb), kinds, keys)
}

// AccountAPIKey resolves the plaintext apiKey an account provider's
// turns must present via requestProviderRuntimeHeaders — config.json
// is where the desktop persists the per-plan keys it applies itself.
// "" = none found (the caller answers headersApplied:false).
func AccountAPIKey(providerID string) string {
	builtin := builtinProviderFor(providerID)
	if builtin == "" {
		return ""
	}
	cb, err := os.ReadFile(homeV2Path("config.json"))
	if err != nil {
		return ""
	}
	return builtinAPIKeys(cb)[builtin]
}

// ListModelProviders is the full pick list the birth/model UIs offer:
// account-plan groups first (the desktop picker's order), personal
// providers after — one ProviderModels row per optgroup.
func ListModelProviders() []ProviderModels {
	out := ListAccountProviders()
	out = append(out, ListPersonalProviders()...)
	return out
}
