package verbs

// envelope.go — the seated face's per-frame decode shape, moved
// verbatim from the shell's session_verbs.go (the verb contract's
// payload half; wiregen extracts it into frame-contract.json wherever
// this file lives under the server family).

import (
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/wire"
)

// Envelope is the seated face's per-frame decode shape: every
// verb's payload slots in one struct — a FRESH zero value each frame
// (encoding/json decodes pointers in place; reuse would leak one
// frame's patch into the next).
type Envelope struct {
	Type string `json:"type"`
	Text string `json:"text"`

	// say origin (v0.6 M1): only "mirror" from the owner's name is
	// honored; anything else falls through as a plain say.
	Origin string `json:"origin,omitempty"`

	// say images (输入图片): media-warehouse references riding the
	// line — uploaded beforehand via POST /media, so the WS frame
	// stays lean. The hub stamps them onto the message (history +
	// broadcast) and the dispatcher injects them as session
	// attachments for @-addressed members.
	Images []wire.Image `json:"images,omitempty"`

	// say quote (结构化引用回复): the quoted line's identity as
	// fields — the body stays clean (复制/转发不再带引用头)。 The
	// hub normalizes it (normQuote) and resolves 引用即点名 by
	// field. Nil on every non-quoted say.
	Quote *chat.Quote `json:"quote,omitempty"`

	// ack (飞书式收到): the say line's seq the member acknowledges.
	Seq int64 `json:"seq,omitempty"`

	// react (飞书式表情回应): the say line's seq (Seq above),
	// the palette emoji and the direction — On adds the dialer's
	// own reaction, absent/false removes it.
	Emoji string `json:"emoji,omitempty"`
	On    bool   `json:"on,omitempty"`

	Task   *wire.Task  `json:"task,omitempty"`
	TaskID string      `json:"task_id,omitempty"`
	Patch  *wire.Patch `json:"patch,omitempty"`

	// task_arbitrate's verdict (owner-only): nil means the frame
	// carried no judgement — refused rather than read as decline.
	Approve *bool `json:"approve,omitempty"`

	Doc *chat.KbDoc `json:"doc,omitempty"`
	Key string      `json:"key,omitempty"`
	Rev int         `json:"rev,omitempty"`

	// kb_write's optional conditional (v0.6 §3.3): nil or negative
	// reads as unset (plain last-writer-wins), 0 means "must not
	// exist yet", >0 must equal the doc's current rev.
	ExpectRev *int `json:"expect_rev,omitempty"`

	Agent *chat.AgentConfig `json:"agent,omitempty"`

	// kick / rank_set targets (v0.6 M2 / v0.8 M1): the member the
	// management frame addresses, plus the rank for rank_set.
	Name string `json:"name,omitempty"`
	Rank int    `json:"rank,omitempty"`

	// skill/mcp verbs: skill_save carries the skill, mcp_save
	// the server; assemble carries the seat/profile target, the
	// list op and the asset keys (its project rides the wire's
	// project key). Via is the CLI rider's audit marker.
	Skill      *chat.Skill     `json:"skill,omitempty"`
	Mcp        *chat.MCPServer `json:"mcp,omitempty"`
	Person     string          `json:"person,omitempty"`
	Target     string          `json:"target,omitempty"`
	Op         string          `json:"op,omitempty"`
	Skills     []string        `json:"skills,omitempty"`
	MCPServers []string        `json:"mcps,omitempty"`
	Project    string          `json:"project,omitempty"`
	Via        string          `json:"via,omitempty"`

	// plan verbs (v2 P4-b): plan_submit carries the proposal (its
	// project_key must be this connection's room); plan_accept /
	// plan_reject name the pending plan (project defaults to this
	// room, the frame's project key overrides for lobby-dialed
	// hosts; accept's revised is the entry-edited full plan).
	Plan    *wire.Plan `json:"plan,omitempty"`
	PlanID  string     `json:"plan_id,omitempty"`
	Revised *wire.Plan `json:"revised,omitempty"`

	// merge verbs (v2.8 gitflow): merge_submit carries the
	// proposal's core（branch 必填，into/req 可选）; merge_accept /
	// merge_reject name the pending merge（project 槽同 plan）.
	Merge   *wire.Merge `json:"merge,omitempty"`
	MergeID string      `json:"merge_id,omitempty"`

	// req verb (全智能模式 v2.8): req_create carries the
	// requirement payload (title/body; project defaults to this
	// connection's room). The private "req" reply is the receipt.
	Req *wire.Req `json:"req,omitempty"`

	// meeting verbs: meeting_extend carries its minutes (0/absent
	// = the 10 default); meeting_end carries nothing.
	Minutes int `json:"minutes,omitempty"`
}
