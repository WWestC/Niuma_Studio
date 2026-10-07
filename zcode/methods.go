package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionInfo is one entry of session/list (fields the dispatcher
// needs; the server sends more).
type SessionInfo struct {
	SessionID   string `json:"sessionId"`
	SessionKind string `json:"sessionKind"`
	// ParentSessionID names the member session a subagent session was
	// spawned for (empty = a root session). The trace tap maps subagent
	// event traffic back onto the owning member through it.
	ParentSessionID string `json:"parentSessionId"`
	Title           string `json:"title"`
	Mode            string `json:"mode"`
	Status          string `json:"status"`
	Workspace       struct {
		WorkspacePath string `json:"workspacePath"`
	} `json:"workspace"`
	UpdatedAt FlexTime `json:"updatedAt"`
}

// FlexTime accepts the wire's timestamp in either shape — the
// app-server sends session.updatedAt as an epoch-ms NUMBER on
// session/resume (and ISO strings elsewhere); a typed string field
// fails the whole ResumeResult unmarshal (the adopt-path bug this
// type closes).
type FlexTime struct{ raw string }

func (f *FlexTime) UnmarshalJSON(b []byte) error {
	f.raw = strings.Trim(string(b), `"`)
	return nil
}

// String returns the raw wire value (ms epoch or ISO — the caller's
// to interpret).
func (f FlexTime) String() string { return f.raw }

// ListSessions lists sessions (limit 50 by default, interactive only).
func (c *Client) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	raw, err := c.callCtx(ctx, "session/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("zcode: list: %w", err)
	}
	return res.Sessions, nil
}

// workspaceObj is the {workspaceKey, workspacePath} pair create and
// resume share.
func workspaceObj(path string) map[string]string {
	return map[string]string{"workspaceKey": path, "workspacePath": path}
}

// CreateSession opens a new session in the workspace. mode is one of
// plan|build|edit|yolo; the dispatcher births members in yolo (M1
// spike note: the mode shown back by session/list may report the
// family default — drive permission policy through the reverse
// handler, not through this field).
func (c *Client) CreateSession(ctx context.Context, workspacePath, mode string) (string, error) {
	return c.CreateSessionMCP(ctx, workspacePath, mode, nil)
}

// MCPServerSpec is one entry of session/create's optional mcpServers
// param: a NON-EMPTY slice REPLACES the session's entire MCP fleet
// (empty/nil falls back to the user-level config's set — the wire
// schema reads an empty array as "unspecified"). Only the http/sse
// shape is modeled; it is all the probe override ever sends. Measured
// on CLI 3.14: a default-fleet session parks its first model request
// ~4s while the fleet's tool surface connects (an npx stdio server
// alone initializes in ~5.6s), so turns that call no tools want a
// bare fleet instead.
type MCPServerSpec struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"` // "http" | "sse"
	URL       string      `json:"url"`
	Headers   []MCPHeader `json:"headers"` // wire wants an array, empty allowed
	Isolation string      `json:"isolation,omitempty"`
	TimeoutMs int         `json:"timeoutMs,omitempty"`
}

// MCPHeader is one {name, value} pair of an MCPServerSpec's headers.
type MCPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CreateSessionMCP is CreateSession with a per-session MCP fleet
// override (nil/empty = the config default; see MCPServerSpec).
func (c *Client) CreateSessionMCP(ctx context.Context, workspacePath, mode string, servers []MCPServerSpec) (string, error) {
	params := map[string]any{
		"workspace": workspaceObj(workspacePath),
		"mode":      mode,
	}
	if len(servers) > 0 {
		params["mcpServers"] = servers
	}
	raw, err := c.callCtx(ctx, "session/create", params)
	if err != nil {
		return "", err
	}
	var res struct {
		SessionID string `json:"sessionId"`
		Session   struct {
			SessionID string `json:"sessionId"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("zcode: create: %w", err)
	}
	if res.Session.SessionID != "" { // nested form is the reliable one
		return res.Session.SessionID, nil
	}
	return res.SessionID, nil
}

// ResumeResult is what session/resume hands back (members' pending
// todos etc. — kept raw where unimportant).
type ResumeResult struct {
	Session SessionInfo `json:"session"`
}

// ResumeSession materializes a persisted session into this
// app-server process (the -32004 → resume → subscribe ladder).
func (c *Client) ResumeSession(ctx context.Context, sessionID string) (*ResumeResult, error) {
	raw, err := c.callCtx(ctx, "session/resume", map[string]any{
		"sessionId": sessionID,
		"workspace": workspaceObj(c.opts.Cwd),
	})
	if err != nil {
		return nil, err
	}
	var res ResumeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("zcode: resume: %w", err)
	}
	return &res, nil
}

// SubscribeResult is the session/subscribe ack.
type SubscribeResult struct {
	SessionID string `json:"sessionId"`
	EventSeq  int64  `json:"eventSeq"`
}

// Subscribe pins the session's event stream to this client
// (deliveryKind "desktop-continuous" — also pins the session against
// idle reclamation in the server). A -32004 answer means the session
// is cold in this process: ResumeSession, then Subscribe again.
func (c *Client) Subscribe(ctx context.Context, sessionID string) (*SubscribeResult, error) {
	return c.SubscribeAfter(ctx, sessionID, 0)
}

// SubscribeAfter subscribes resuming from an event cursor.
func (c *Client) SubscribeAfter(ctx context.Context, sessionID string, afterSeq int64) (*SubscribeResult, error) {
	params := map[string]any{
		"sessionId":    sessionID,
		"deliveryKind": "desktop-continuous",
	}
	if afterSeq > 0 {
		params["afterSeq"] = afterSeq
	}
	raw, err := c.callCtx(ctx, "session/subscribe", params)
	if err != nil {
		return nil, err
	}
	var res SubscribeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("zcode: subscribe: %w", err)
	}
	return &res, nil
}

// SendAck is session/send's asynchronous acceptance — the model turn
// itself plays out over the event stream.
type SendAck struct {
	SessionID     string `json:"sessionId"`
	Accepted      bool   `json:"accepted"`
	StateRevision int64  `json:"stateRevision"`
}

// Attachment is one file riding a send as an attachment (the
// app-server's session/send attachments param, ZCode ≥3.14): Kind is
// image|pdf|video|file, and the payload is either a LocalPath the
// child reads itself (the media warehouse's shape — no base64 on the
// wire) or DataBase64. Filename/MimeType/SizeBytes are descriptive;
// the app-server fills what the model layer needs from them.
type Attachment struct {
	Kind       string `json:"kind"`
	LocalPath  string `json:"localPath,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
	Filename   string `json:"filename,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
}

// Send injects content as the session's next user input (the
// "simulate the user typing" primitive). inputID, when non-empty, is
// the caller's idempotency key (whitelist key; M1 spike: accepted,
// duplicate-while-busy still answers -32010 — treat busy as busy and
// re-drive from the dispatcher's durable inbox, do not rely on
// server-side dedup). deny, when non-empty, rides the toolDenylist
// whitelist key: tools this session must not call (additive — omitted
// when empty, servers that don't know the key ignore it).
func (c *Client) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*SendAck, error) {
	return c.SendAttached(ctx, sessionID, content, inputID, deny, nil)
}

// SendAttached is Send with attachments (ZCode ≥3.14's session/send
// param — images the member's turn sees as real image blocks, not
// textual references). atts empty answers byte-identical to Send, so
// callers can hand both through one path.
func (c *Client) SendAttached(ctx context.Context, sessionID, content, inputID string, deny []string, atts []Attachment) (*SendAck, error) {
	params := map[string]any{"sessionId": sessionID, "content": content}
	if inputID != "" {
		params["inputId"] = inputID
	}
	if len(deny) > 0 {
		params["toolDenylist"] = deny
	}
	if len(atts) > 0 {
		params["attachments"] = atts
	}
	raw, err := c.callCtx(ctx, "session/send", params)
	if err != nil {
		return nil, err
	}
	var ack SendAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		return nil, fmt.Errorf("zcode: send: %w", err)
	}
	return &ack, nil
}

// Steer injects content into the running turn's next step boundary
// (session/steer — same params as send).
func (c *Client) Steer(ctx context.Context, sessionID, content string) error {
	_, err := c.callCtx(ctx, "session/steer", map[string]any{
		"sessionId": sessionID, "content": content,
	})
	return err
}

// ProbeSession touches the session with a minimal read-only request
// (session/events, limit 1) — one RPC, two jobs:
//
//   - KEEPALIVE: the app-server's resident pool counts every
//     sessionId-carrying request — an operation lease while it runs, a
//     touch on release — and a touch resets the session's 10-minute
//     idle-reap eligibility. A member turn that only emits subagent
//     traffic (the art-designer pattern: the parent parks while a
//     subagent reads images) otherwise goes untouched long enough for
//     the reaper to tear the session down mid-turn.
//   - LIVENESS: -32004 answers that the session is cold in this
//     process (reaped, or the child was swapped) — the dispatcher
//     watchdog's resurrect cue. Any other error is bridge trouble, not
//     a verdict on the session.
func (c *Client) ProbeSession(ctx context.Context, sessionID string) error {
	_, err := c.callCtx(ctx, "session/events", map[string]any{
		"sessionId": sessionID, "limit": 1,
	})
	return err
}

// SetMode switches the session's permission mode (plan|build|edit|yolo).
func (c *Client) SetMode(ctx context.Context, sessionID, mode string) error {
	_, err := c.callCtx(ctx, "session/setMode", map[string]any{
		"sessionId": sessionID, "mode": mode,
	})
	return err
}

// Compact folds the session's history into a summary (session/compact —
// the programmatic /compact). instructions, when non-empty, steer what
// the summary keeps ("保留在办任务与关键决定…" — the P0 fold's own
// brief); empty rides the built-in default. The server refuses while a
// prompt is running and answers "already_running" if a compact is in
// flight — both are the caller's retry/cooldown discipline, not errors
// to surface.
func (c *Client) Compact(ctx context.Context, sessionID, instructions string) error {
	params := map[string]any{"sessionId": sessionID}
	if instructions != "" {
		params["instructions"] = instructions
	}
	_, err := c.callCtx(ctx, "session/compact", params)
	return err
}

// ModelSelection is the {providerId, modelId, reasoning} triple the
// protocol wants wherever a model is chosen. M1 spike F: passing this
// inside session/create triggers a CLI-side persistence bug (FK
// constraint) that kills every turn — select the model AFTER birth
// via SetModel instead (spike G formula).
type ModelSelection struct {
	ProviderID string        `json:"providerId"`
	ModelID    string        `json:"modelId"`
	Options    *ModelOptions `json:"options,omitempty"`
}

// ModelOptions carries the reasoning level some models require
// (GLM-5.3 on the personal relay rejects without it).
type ModelOptions struct {
	ReasoningLevel string `json:"reasoningLevel,omitempty"`
}

// SetModel selects the session's model. The birth formula (spike G):
// CreateSession WITHOUT a model, then SetModel, then Subscribe/Send.
func (c *Client) SetModel(ctx context.Context, sessionID string, sel ModelSelection) error {
	_, err := c.callCtx(ctx, "session/setModel", map[string]any{
		"sessionId": sessionID, "model": sel,
	})
	return err
}

// ResolveModelProvider is the ORDERED default-model resolution: it
// walks the pick list's order (ListModelProviders — account-plan faces
// first, the desktop picker's own order, personal providers after) and
// returns the first DRIVABLE face that lists the model. The default
// model thus lands where the desktop picker would put it — a machine
// with a selected, keyed coding-plan face AND a dead personal relay
// probes and asks on the plan, not on the relay (房主实录 2026-10-07:
// 智谱内部 pointed at a down local proxy and every default turn hung).
// Faces the composition marks unavailable (no key / connection not
// selected / no entitled models) are never auto-picked — the picker
// renders them disabled; the runtime must not serve them. When no
// drivable face lists the model, the legacy answer stands: the first
// personal provider, so SetModel is the one to say why that pin fails.
func ResolveModelProvider(modelID string) (providerID string, ok bool) {
	if id, ok := resolveModelProviderIn(ListModelProviders(), modelID); ok {
		return id, true
	}
	return ResolvePersonalProvider(modelID)
}

// resolveModelProviderIn is the ordered walk's pure half: the first
// DRIVABLE face in list order (account before personal — the caller
// hands ListModelProviders rows) that lists the model wins; a miss
// reports false so the caller takes the legacy personal pin.
func resolveModelProviderIn(rows []ProviderModels, modelID string) (string, bool) {
	for _, p := range rows {
		if !p.Available {
			continue // the picker shows it disabled; never auto-pick it
		}
		if modelID == "" {
			return p.ProviderID, true // parity: production callers always name a model
		}
		for _, m := range p.Models {
			if m == modelID {
				return p.ProviderID, true
			}
		}
	}
	return "", false
}

// ResolvePersonalProvider reads ~/.zcode/v2/provider_config.json
// (utf-8, BOM-tolerant) and returns the first personal provider's id
// plus whether it lists the wanted model — the personal half of the
// resolution, kept as ResolveModelProvider's legacy tail and as the
// seam tests inject. Empty result = no personal provider configured.
func ResolvePersonalProvider(modelID string) (providerID string, ok bool) {
	rules := personalRules()
	if len(rules) == 0 {
		return "", false
	}
	id := rules[0].ProviderID
	if modelID == "" {
		return id, true
	}
	for _, m := range rules[0].Models {
		if m == modelID {
			return id, true
		}
	}
	return id, false
}

// ProviderModels is one provider's pickable surface: the config's
// display name (e.g. "智谱内部", "BigModel Team Coding Plan") plus the
// model ids in the operator's chosen order. Badge carries the plan
// chip the desktop picker renders for account-plan faces
// (团队/免费/个人); personal providers leave it empty. Available is
// the local drivability verdict — the desktop registry keeps faces it
// can't currently drive and marks them unselectable (never drops
// them), so the composition carries the verdict as data for the UIs
// to render disabled; Unavailable holds the reason token when
// Available is false (not-connected / connection-unselected /
// no-entitled-models).
type ProviderModels struct {
	ProviderID   string
	ProviderName string
	Badge        string
	Models       []string
	Available    bool
	Unavailable  string
}

// ListPersonalProviders reads ~/.zcode/v2/provider_config.json and
// returns EVERY personal provider rule with its models — the pick
// list the birth/model UIs offer (modelOrder preferred, personalModelIds
// the fallback; deduped, order preserved). Empty = nothing configured.
func ListPersonalProviders() []ProviderModels {
	rules := personalRules()
	out := make([]ProviderModels, 0, len(rules))
	for _, r := range rules {
		if r.ProviderID == "" || len(r.Models) == 0 {
			continue
		}
		out = append(out, ProviderModels{
			ProviderID:   r.ProviderID,
			ProviderName: r.ProviderName,
			Models:       r.Models,
			Available:    true, // personal rules carry their own apiKey — always drivable
		})
	}
	return out
}

// personalRule is one flattened providerConfigRules.providerRules row
// (only the fields the pick list needs).
type personalRule struct {
	ProviderID   string
	ProviderName string
	Models       []string
}

// personalRules parses provider_config.json (missing/unreadable/garbled
// = no rules, never an error — the callers degrade).
func personalRules() []personalRule {
	path, err := HomePathV2("provider_config.json")
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parsePersonalRules(b)
}

// parsePersonalRules is personalRules' pure half (BOM-tolerant):
// every rule with ProviderID + deduped models, modelOrder preferred
// over personalModelIds. Test surface; the file half never branches.
func parsePersonalRules(b []byte) []personalRule {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var cfg struct {
		Config struct {
			ProviderConfigRules struct {
				ProviderRules []struct {
					ProviderID   string `json:"providerId"`
					ProviderName string `json:"providerName"`
					Config       struct {
						PersonalModelIDs []string `json:"personalModelIds"`
						ModelOrder       []string `json:"modelOrder"`
					} `json:"config"`
				} `json:"providerRules"`
			} `json:"providerConfigRules"`
		} `json:"config"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	rules := cfg.Config.ProviderConfigRules.ProviderRules
	out := make([]personalRule, 0, len(rules))
	for _, r := range rules {
		models := r.Config.ModelOrder
		if len(models) == 0 {
			models = r.Config.PersonalModelIDs
		}
		seen := make(map[string]struct{}, len(models))
		uniq := make([]string, 0, len(models))
		for _, m := range models {
			if m == "" {
				continue
			}
			if _, dup := seen[m]; dup {
				continue
			}
			seen[m] = struct{}{}
			uniq = append(uniq, m)
		}
		out = append(out, personalRule{
			ProviderID: r.ProviderID, ProviderName: r.ProviderName, Models: uniq,
		})
	}
	return out
}

// ParseModelRef splits a seat/model pick into its provider and model
// halves: "providerId/modelId" carries its own provider, a bare id
// resolves through the personal-provider config at apply time. The
// provider half wins even when it names no configured rule — the
// explicit pick is trusted as written (SetModel answers whether it
// lands).
func ParseModelRef(ref string) (providerID, modelID string) {
	ref = strings.TrimSpace(ref)
	if i := strings.IndexByte(ref, '/'); i > 0 && i < len(ref)-1 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

// HomePathV2 resolves a path under ~/.zcode/v2.
func HomePathV2(rel string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zcode", "v2", rel), nil
}

// callCtx wraps call with context cancellation (the wire itself has
// no cancellation; this abandons the wait, the late answer is
// dropped with it).
func (c *Client) callCtx(ctx context.Context, method string, params any) (json.RawMessage, error) {
	type wrap struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan wrap, 1)
	go func() {
		raw, err := c.call(method, params)
		ch <- wrap{raw, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case w := <-ch:
		return w.raw, w.err
	}
}
