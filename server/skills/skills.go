package skills

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

// Face is the capability-assembly domain's whole world: the library,
// the staffing/audit/plugin stores it orchestrates, the task ranks its
// permission floors read, the fleet it pokes on assembly, and the
// routing/local identity — split from the shell's skills.go.
type Face struct {
	Stores   verbs.StoreSet
	Fleet    verbs.FleetAPI
	Local    string
	Hub      *chat.Hub
	Registry *chat.Registry
}

// FaceOf adapts a seat (the verb contract's view) into this face —
// domain verbs and the shell's HTTP mounts drink the same one.
func FaceOf(st *verbs.Seat) *Face {
	return &Face{Stores: st.Stores, Fleet: st.Fleet, Local: st.Local, Hub: st.Hub, Registry: st.Registry}
}

// assembleReq is one C->S assemble verb, fielded into a struct. The
// op applies to BOTH lists at once (one op, one receipt, one audit
// row — an assembly is one organizational act).
type AssembleReq struct {
	Project    string
	Person     string
	Target     string // capability.TargetStaffing | capability.TargetProfile
	Op         string // capability.OpSet | OpAdd | OpRemove
	Skills     []string
	MCPServers []string
}

// applyListOp folds op into the current list: set replaces, add
// appends the missing in order, remove subtracts.
func applyListOp(cur []string, op string, keys []string) []string {
	switch op {
	case capability.OpAdd:
		out := append([]string(nil), cur...)
		for _, k := range keys {
			if !keyIn(out, k) {
				out = append(out, k)
			}
		}
		return out
	case capability.OpRemove:
		out := make([]string, 0, len(cur))
		for _, k := range cur {
			if !keyIn(keys, k) {
				out = append(out, k)
			}
		}
		return out
	default: // set
		return append([]string(nil), keys...)
	}
}

func keyIn(list []string, key string) bool {
	for _, k := range list {
		if k == key {
			return true
		}
	}
	return false
}

// viaOf normalizes the audit's surface column: the CLI rider marks
// itself, everything else on the wire reads as "ws".
func viaOf(via string) string {
	if via == "" {
		return "ws"
	}
	return via
}

// skillAllowed is the assembly governance (unchanged from the pack
// era): the host assembles anyone; Lv.8+ may act on seats ranked at
// least two below themselves (the −2 delegation ceiling). The floor
// makes self-assembly and peer-assembly structurally impossible —
// capability is organizational configuration, not a self-serve
// shelf.
func (fc *Face) skillAllowed(actor, person string) bool {
	if actor == fc.Local {
		return true
	}
	if fc.Stores.Engine == nil {
		return false
	}
	ar := fc.Stores.Engine.Rank(actor)
	if ar < verbs.KickMinRank {
		return false
	}
	return fc.Stores.Engine.Rank(person) <= ar-2
}

// skillSaveAllowed gates the library writes: the host, or Lv.8+ — a
// library edit has no target seat, so the −2 half of the rule has
// nothing to bind to.
func (fc *Face) skillSaveAllowed(actor string) bool {
	if actor == fc.Local {
		return true
	}
	return fc.Stores.Engine != nil && fc.Stores.Engine.Rank(actor) >= verbs.KickMinRank
}

// assembleAs applies one assemble op on behalf of actor into room h's
// sight and returns the receipt frame: a reminder-only skill
// {event:"assembled"} broadcast on success (also returned for the
// seatless channel to write back), a private skill{event:"denied"} on
// refusal. Stores change only after every check passes — an over-cap
// or unknown-target assembly leaves nothing behind: no store write,
// no audit row, no dispatcher poke.
func (fc *Face) AssembleAs(h *chat.Hub, actor string, req AssembleReq, via string) chat.Message {
	deny := func(reason string) chat.Message {
		return chat.Message{Type: chat.MsgSkillEvt, Event: "denied",
			Project: req.Project, Person: req.Person, From: actor,
			Text: reason, TS: time.Now().Unix()}
	}
	if req.Person == "" {
		return deny(i18n.S("缺少目标成员名（person）"))
	}
	if req.Target == "" {
		req.Target = capability.TargetStaffing
	}
	if req.Op == "" {
		req.Op = capability.OpSet
	}
	switch req.Target {
	case capability.TargetStaffing, capability.TargetProfile:
	default:
		return deny(i18n.Sf("未知装配目标 %q（staffing=岗位覆写 / profile=档案默认）", req.Target))
	}
	switch req.Op {
	case capability.OpSet, capability.OpAdd, capability.OpRemove:
	default:
		return deny(i18n.Sf("未知装配操作 %q（set|add|remove）", req.Op))
	}
	if fc.Stores.Library == nil {
		return deny(i18n.S("技能装配未启用（技能库不可用）"))
	}
	if fc.Stores.AgentStore == nil {
		return deny(i18n.S("档案库未启用"))
	}
	if !fc.skillAllowed(actor, req.Person) {
		return deny(i18n.Sf("装配被拒：房主任意，Lv.8+ 仅可操作等级低于自身 −2 的岗（%s 不满足；能力是组织配置，成员不能自装）", actor))
	}
	cfg, ok := fc.Stores.AgentStore.Get(req.Person)
	if !ok {
		return deny(i18n.Sf("%s 未建档", req.Person))
	}

	var curSkills, curMCPs []string
	switch req.Target {
	case capability.TargetStaffing:
		if fc.Stores.StaffStore == nil {
			return deny(i18n.S("编制表未启用（岗位装配不可用）"))
		}
		if fc.Registry == nil {
			return deny(i18n.S("多办公室未启用（项目岗不可用；profile 目标可装配档案默认）"))
		}
		if _, err := fc.Registry.Hub(req.Project); err != nil {
			return deny(i18n.Sf("项目 %s 不可用：%v", req.Project, err))
		}
		row, ok := fc.Stores.StaffStore.Get(req.Project, req.Person)
		if !ok {
			return deny(i18n.Sf("编制行不存在：%s 在 %s", req.Person, req.Project))
		}
		curSkills, curMCPs = row.Skills, row.MCPs
	default: // profile
		curSkills, curMCPs = cfg.Skills, cfg.MCPs
	}
	finalSkills := applyListOp(curSkills, req.Op, req.Skills)
	finalMCPs := applyListOp(curMCPs, req.Op, req.MCPServers)

	// The overflow gate runs BEFORE anything changes: an over-cap
	// assembly is refused whole, not written and degraded later. The
	// compose folds the same two levels the seat will run on — a
	// profile-target op under a non-empty staffing override composes
	// the override (that IS the seat's fabric).
	profileSkills, profileMCPs := cfg.Skills, cfg.MCPs
	seatSkills, seatMCPs := finalSkills, finalMCPs
	if req.Target == capability.TargetProfile {
		profileSkills, profileMCPs = finalSkills, finalMCPs
		seatSkills, seatMCPs = nil, nil
		if fc.Stores.StaffStore != nil {
			if row, ok := fc.Stores.StaffStore.OccupancyOf(req.Person); ok {
				seatSkills, seatMCPs = row.Skills, row.MCPs
			}
		}
	}
	skills := capability.EffectiveSkills(profileSkills, seatSkills)
	mcps := capability.EffectiveMCPs(profileMCPs, seatMCPs)
	fabric, err := capability.Compose(cfg,
		capability.ResolveSkills(fc.Stores.Library, skills),
		capability.ResolveMCPs(fc.Stores.Library, mcps),
		fc.Stores.Library.ModelAuthoring())
	if err != nil {
		return deny(i18n.Sf("装配被拒：%v", err))
	}

	switch req.Target {
	case capability.TargetStaffing:
		if _, err := fc.Stores.StaffStore.SetSkills(req.Project, req.Person, finalSkills); err != nil {
			return deny(err.Error())
		}
		if _, err := fc.Stores.StaffStore.SetMCPs(req.Project, req.Person, finalMCPs); err != nil {
			return deny(err.Error())
		}
	default:
		if !fc.Stores.AgentStore.SetSkills(req.Person, finalSkills) {
			return deny(i18n.Sf("%s 未建档", req.Person))
		}
		if !fc.Stores.AgentStore.SetMCPs(req.Person, finalMCPs) {
			return deny(i18n.Sf("%s 未建档", req.Person))
		}
	}

	// Effect notification: poke the member's live dispatcher — the
	// effect matrix lands the 【技能更新】 header and the model pick.
	// A member not currently dispatched just composes fresh at the
	// next birth; the receipt says so, never refuses — the stores are
	// already written.
	note := ""
	if fc.Fleet != nil {
		if err := fc.Fleet.Assemble(req.Target, req.Project, req.Person); err != nil {
			note = i18n.Sf("（%v——已落账，接入后生效）", err)
		}
	}

	projKey := ""
	if req.Target == capability.TargetStaffing {
		projKey = req.Project
	}
	digest := fabric.Digest()
	fc.Stores.Audit.Append(capability.AssemblyEvent{
		Actor: actor, ProjectKey: projKey, Person: req.Person,
		Target: req.Target, Op: req.Op,
		Skills: finalSkills, MCPServers: finalMCPs,
		Effective: &digest, Via: viaOf(via),
	})

	text := fc.assembleReceipt(req, finalSkills, finalMCPs) + note
	msg := chat.Message{Type: chat.MsgSkillEvt, Event: "assembled",
		Project: projKey, Person: req.Person, Target: req.Target,
		Skills: finalSkills, MCPServers: finalMCPs,
		From: actor, Text: text, TS: time.Now().Unix()}
	h.Broadcast(msg)
	return msg
}

// assembleReceipt renders the human summary: skill list, MCP list
// (flagged 下次出生生效 when non-empty), and the missing-key notes.
func (fc *Face) assembleReceipt(req AssembleReq, finalSkills, finalMCPs []string) string {
	var parts []string
	if list := strings.Join(finalSkills, "、"); list != "" {
		parts = append(parts, i18n.Sf("技能：%s", list))
	} else {
		parts = append(parts, i18n.S("技能：无"))
	}
	if list := strings.Join(finalMCPs, "、"); list != "" {
		parts = append(parts, i18n.Sf("MCP：%s（下次出生生效）", list))
	} else {
		parts = append(parts, i18n.S("MCP：无"))
	}
	text := i18n.Sf("已为 %s %s装配——%s", req.Person, targetLabel(req.Target), strings.Join(parts, "；"))
	if missing := missingSkillKeys(fc.Stores.Library, finalSkills); len(missing) > 0 {
		text += i18n.Sf("（技能未入库，暂不生效：%s）", strings.Join(missing, "、"))
	}
	if missing := missingMCPKeys(fc.Stores.Library, finalMCPs); len(missing) > 0 {
		text += i18n.Sf("（MCP 未入库，暂不生效：%s）", strings.Join(missing, "、"))
	}
	return text
}

func targetLabel(target string) string {
	if target == capability.TargetProfile {
		return i18n.S("的档案默认")
	}
	return i18n.S("的岗位")
}

// skillSaveAs upserts one skill on behalf of actor and returns the
// receipt frame: a reminder-only skill{event:"saved"} broadcast on
// success (the summary carries every field except the body — the
// detail stays behind GET /skills/{key}), a private
// skill{event:"denied"} on refusal.
func (fc *Face) SkillSaveAs(h *chat.Hub, actor string, sk *chat.Skill, via string) chat.Message {
	key := ""
	if sk != nil {
		key = sk.Key
	}
	deny := func(reason string) chat.Message {
		return chat.Message{Type: chat.MsgSkillEvt, Event: "denied",
			Skill: &chat.Skill{Key: key}, From: actor,
			Text: reason, TS: time.Now().Unix()}
	}
	if sk == nil {
		return deny(i18n.S("缺少技能载荷（skill）"))
	}
	if fc.Stores.Library == nil {
		return deny(i18n.S("技能装配未启用（技能库不可用）"))
	}
	if !fc.skillSaveAllowed(actor) {
		return deny(i18n.Sf("技能库写入被拒：房主或 Lv.%d 及以上可写（%s 不满足）", verbs.KickMinRank, actor))
	}
	saved, err := fc.Stores.Library.UpsertSkill(capability.Skill{
		Key: sk.Key, Name: sk.Name, Desc: sk.Desc,
		Body: sk.Body, Manuals: sk.Manuals,
		CreatedBy: actor, // stamped on create only; updates keep the origin
	})
	if err != nil {
		return deny(err.Error())
	}
	msg := chat.Message{Type: chat.MsgSkillEvt, Event: "saved",
		Skill: &chat.Skill{
			Key: saved.Key, Name: saved.Name, Desc: saved.Desc,
			Manuals: saved.Manuals, CreatedBy: saved.CreatedBy, CreatedTS: saved.CreatedTS,
		},
		From: actor,
		Text: i18n.Sf("已保存技能 %s（%s）", saved.Key, saved.Name),
		TS:   time.Now().Unix()}
	h.Broadcast(msg)
	return msg
}

// mcpSaveAs upserts one MCP server on behalf of actor. Header values
// never ride the broadcast — the masked form does.
func (fc *Face) McpSaveAs(h *chat.Hub, actor string, m *chat.MCPServer, via string) chat.Message {
	key := ""
	if m != nil {
		key = m.Key
	}
	deny := func(reason string) chat.Message {
		return chat.Message{Type: chat.MsgMcpEvt, Event: "denied",
			Mcp: &chat.MCPServer{Key: key}, From: actor,
			Text: reason, TS: time.Now().Unix()}
	}
	if m == nil {
		return deny(i18n.S("缺少 MCP 服务载荷（mcp）"))
	}
	if fc.Stores.Library == nil {
		return deny(i18n.S("技能装配未启用（技能库不可用）"))
	}
	if !fc.skillSaveAllowed(actor) {
		return deny(i18n.Sf("技能库写入被拒：房主或 Lv.%d 及以上可写（%s 不满足）", verbs.KickMinRank, actor))
	}
	in := capability.MCPServer{
		Key: m.Key, Name: m.Name, Desc: m.Desc,
		Type: m.Type, URL: m.URL,
		CreatedBy: actor,
	}
	for _, h := range m.Headers {
		in.Headers = append(in.Headers, capability.MCPHeader{Name: h.Name, Value: h.Value})
	}
	saved, err := fc.Stores.Library.UpsertMCP(in)
	if err != nil {
		return deny(err.Error())
	}
	msg := chat.Message{Type: chat.MsgMcpEvt, Event: "saved",
		Mcp:  mcpWireSummary(saved),
		From: actor,
		Text: i18n.Sf("已保存 MCP 服务 %s（%s，%s）", saved.Key, saved.Name, saved.URL),
		TS:   time.Now().Unix()}
	h.Broadcast(msg)
	return msg
}

// mcpWireSummary builds the broadcast-safe wire shape: masked header
// values, provenance kept.
func mcpWireSummary(m capability.MCPServer) *chat.MCPServer {
	out := &chat.MCPServer{
		Key: m.Key, Name: m.Name, Desc: m.Desc, Type: m.Type, URL: m.URL,
		CreatedBy: m.CreatedBy, CreatedTS: m.CreatedTS,
	}
	for _, h := range m.MaskedHeaders() {
		out.Headers = append(out.Headers, chat.MCPHeader{Name: h.Name, Value: h.Value})
	}
	return out
}

// missingSkillKeys names the keys an assembly references that the
// skill library no longer holds — they degrade to nothing at compose
// time, but the receipt says so instead of failing silently.
func missingSkillKeys(store *capability.Store, keys []string) []string {
	var missing []string
	for _, k := range keys {
		if _, ok := store.GetSkill(k); !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// missingMCPKeys is missingSkillKeys for the server list.
func missingMCPKeys(store *capability.Store, keys []string) []string {
	var missing []string
	for _, k := range keys {
		if _, ok := store.GetMCP(k); !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// --- HTTP faces --------------------------------------------------------

// handleSkills lists the skill library (GET /skills): summaries with
// the manual refs and the body's byte weight — the body itself stays
// behind the detail endpoint.
func (fc *Face) HandleSkills(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/skills" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if fc.Stores.Library == nil {
		http.NotFound(w, r)
		return
	}
	out := []map[string]any{}
	for _, sk := range fc.Stores.Library.ListSkills() {
		row := map[string]any{
			"key": sk.Key, "name": sk.Name, "body_bytes": len(sk.Body),
		}
		if sk.Desc != "" {
			row["desc"] = sk.Desc
		}
		if len(sk.Manuals) > 0 {
			row["manuals"] = sk.Manuals
		}
		if sk.CreatedBy != "" {
			row["created_by"] = sk.CreatedBy
			row["created_ts"] = sk.CreatedTS
		}
		out = append(out, row)
	}
	httputil.WriteJSON(w, out)
}

// handleSkill serves one skill (GET /skills/{key}) and the
// host-channel upsert (POST): the loopback trust model makes the HTTP
// writer the local human — the WS verb is the seated/mirror path, the
// CLI rides it. The path is the identity: a body key mismatching the
// path never wins.
func (fc *Face) HandleSkill(w http.ResponseWriter, r *http.Request) {
	if fc.Stores.Library == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if !capability.ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		sk, ok := fc.Stores.Library.GetSkill(key)
		if !ok {
			http.NotFound(w, r)
			return
		}
		httputil.WriteJSON(w, sk)
	case http.MethodPost:
		var sk capability.Skill
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&sk); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
			return
		}
		sk.Key = key
		if fc.Local != "" {
			sk.CreatedBy = fc.Local
		}
		saved, err := fc.Stores.Library.UpsertSkill(sk)
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// observability only: the lobby hears the library moved; the
		// body stays out of the frame (the kb/agent broadcast habit).
		fc.Hub.Broadcast(chat.Message{Type: chat.MsgSkillEvt, Event: "saved",
			Skill: &chat.Skill{Key: saved.Key, Name: saved.Name,
				CreatedBy: saved.CreatedBy, CreatedTS: saved.CreatedTS},
			From: fc.Local,
			Text: i18n.Sf("已保存技能 %s（%s，经 HTTP）", saved.Key, saved.Name),
			TS:   time.Now().Unix()})
		httputil.WriteJSON(w, saved)
	case http.MethodDelete:
		sk, ok := fc.Stores.Library.GetSkill(key)
		if !ok {
			httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("技能 %s 不存在", key))
			return
		}
		refs := fc.skillReferences(key)
		if !fc.Stores.Library.RemoveSkill(key) {
			httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("技能 %s 不存在", key))
			return
		}
		text := i18n.Sf("已删除技能 %s（%s）", key, sk.Name)
		if len(refs) > 0 {
			text = i18n.Sf("已删除技能 %s（%s）——仍在装配中，席位上已失效：%s", key, sk.Name, strings.Join(refs, "、"))
		}
		fc.Hub.Broadcast(chat.Message{Type: chat.MsgSkillEvt, Event: "removed",
			Skill: &chat.Skill{Key: key, Name: sk.Name},
			From:  fc.Local, Text: text, TS: time.Now().Unix()})
		httputil.WriteJSON(w, map[string]any{"ok": true, "key": key, "referenced_by": refs})
	default:
		http.Error(w, "GET/POST/DELETE only", http.StatusMethodNotAllowed)
	}
}

// handleAuthoring serves the studio-wide 自制技能 switch (GET/POST
// /skills/authoring): GET returns {on}; POST {on:bool} flips it in
// the skills store and persists with the library. Loopback trust —
// the same door every studio-wide settings file uses.
func (fc *Face) HandleAuthoring(w http.ResponseWriter, r *http.Request) {
	if fc.Stores.Library == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		httputil.WriteJSON(w, map[string]any{"on": fc.Stores.Library.ModelAuthoring()})
	case http.MethodPost:
		var in struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
			return
		}
		fc.Stores.Library.SetModelAuthoring(in.On)
		httputil.WriteJSON(w, map[string]any{"on": fc.Stores.Library.ModelAuthoring()})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}

// handleMCPs lists the MCP library (GET /mcps) with masked header
// values — the cleartext values stay behind the loopback detail read.
func (fc *Face) HandleMCPs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcps" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if fc.Stores.Library == nil {
		http.NotFound(w, r)
		return
	}
	out := []map[string]any{}
	for _, m := range fc.Stores.Library.ListMCPs() {
		row := map[string]any{
			"key": m.Key, "name": m.Name, "type": m.Type, "url": m.URL,
		}
		if m.Desc != "" {
			row["desc"] = m.Desc
		}
		if len(m.Headers) > 0 {
			hs := []map[string]string{}
			for _, h := range m.MaskedHeaders() {
				hs = append(hs, map[string]string{"name": h.Name, "value": h.Value})
			}
			row["headers"] = hs
		}
		if m.CreatedBy != "" {
			row["created_by"] = m.CreatedBy
			row["created_ts"] = m.CreatedTS
		}
		out = append(out, row)
	}
	httputil.WriteJSON(w, out)
}

// handleMCP serves one MCP server (GET /mcps/{key}, cleartext values
// — loopback read face，安全核查修复后是真检查而非仅靠绑定地址) and
// the host-channel upsert (POST).
func (fc *Face) HandleMCP(w http.ResponseWriter, r *http.Request) {
	if fc.Stores.Library == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if !capability.ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		// 明文头读面与 /owner/token 同规：回环以外的来源裸 404（不确认
		// 存在性）。列表面走 MaskedHeaders，这里的明文只给本机工作台。
		if !httputil.LoopbackAddr(r.RemoteAddr) {
			http.NotFound(w, r)
			return
		}
		m, ok := fc.Stores.Library.GetMCP(key)
		if !ok {
			http.NotFound(w, r)
			return
		}
		httputil.WriteJSON(w, m)
	case http.MethodPost:
		var m capability.MCPServer
		if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&m); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
			return
		}
		m.Key = key
		if fc.Local != "" {
			m.CreatedBy = fc.Local
		}
		saved, err := fc.Stores.Library.UpsertMCP(m)
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
			return
		}
		fc.Hub.Broadcast(chat.Message{Type: chat.MsgMcpEvt, Event: "saved",
			Mcp:  mcpWireSummary(saved),
			From: fc.Local,
			Text: i18n.Sf("已保存 MCP 服务 %s（%s，经 HTTP）", saved.Key, saved.Name),
			TS:   time.Now().Unix()})
		httputil.WriteJSON(w, saved)
	case http.MethodDelete:
		m, ok := fc.Stores.Library.GetMCP(key)
		if !ok {
			httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("MCP 服务 %s 不存在", key))
			return
		}
		refs := fc.mcpReferences(key)
		if !fc.Stores.Library.RemoveMCP(key) {
			httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("MCP 服务 %s 不存在", key))
			return
		}
		text := i18n.Sf("已删除 MCP 服务 %s（%s）", key, m.Name)
		if len(refs) > 0 {
			text = i18n.Sf("已删除 MCP 服务 %s（%s）——仍在装配中，成员出生时不再挂载：%s", key, m.Name, strings.Join(refs, "、"))
		}
		fc.Hub.Broadcast(chat.Message{Type: chat.MsgMcpEvt, Event: "removed",
			Mcp:  &chat.MCPServer{Key: key, Name: m.Name},
			From: fc.Local, Text: text, TS: time.Now().Unix()})
		httputil.WriteJSON(w, map[string]any{"ok": true, "key": key, "referenced_by": refs})
	default:
		http.Error(w, "GET/POST/DELETE only", http.StatusMethodNotAllowed)
	}
}

// skillReferences scans every assembly surface still naming the key —
// staffing overrides (per seat) and profile defaults — as
// "小测（default 编制）" style labels. Read-only best effort: a nil
// store just contributes no references.
func (fc *Face) skillReferences(key string) []string {
	var refs []string
	if fc.Stores.StaffStore != nil {
		for _, row := range fc.Stores.StaffStore.List() {
			if keyIn(row.Skills, key) {
				refs = append(refs, i18n.Sf("%s（%s 编制）", row.Person, row.ProjectKey))
			}
		}
	}
	if fc.Stores.AgentStore != nil {
		for _, cfg := range fc.Stores.AgentStore.List() {
			if keyIn(cfg.Skills, key) {
				refs = append(refs, i18n.Sf("%s（档案默认）", cfg.Name))
			}
		}
	}
	return refs
}

// mcpReferences is skillReferences for the MCP server list.
func (fc *Face) mcpReferences(key string) []string {
	var refs []string
	if fc.Stores.StaffStore != nil {
		for _, row := range fc.Stores.StaffStore.List() {
			if keyIn(row.MCPs, key) {
				refs = append(refs, i18n.Sf("%s（%s 编制）", row.Person, row.ProjectKey))
			}
		}
	}
	if fc.Stores.AgentStore != nil {
		for _, cfg := range fc.Stores.AgentStore.List() {
			if keyIn(cfg.MCPs, key) {
				refs = append(refs, i18n.Sf("%s（档案默认）", cfg.Name))
			}
		}
	}
	return refs
}

// handleStaffingFabric previews one seat's effective fabric (GET
// /p/{key}/staffing/{person}/fabric): Compose over the profile defaults
// ⊕ the staffing overrides — the same single source birth and the audit
// digest read, so the web preview never re-implements assembly.
// Read-only and seatless: the room keeps serving.
func (fc *Face) HandleStaffingFabric(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if fc.Registry == nil || fc.Stores.AgentStore == nil || fc.Stores.Library == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if _, err := fc.Registry.Hub(key); err != nil {
		httputil.WriteJSONErr(w, http.StatusNotFound, err.Error())
		return
	}
	person := r.PathValue("person")
	cfg, ok := fc.Stores.AgentStore.Get(person)
	if !ok {
		// A live seat without a saved config (an external joiner, or a
		// collision-deduped "-2" twin) is a real UI state, not a dead
		// link: say why in a JSON body the panel can render instead of
		// the bare "404 page not found".
		httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("%s 没有已保存档案——无档案即无基座面料可组合（先出生/保存该成员）", person))
		return
	}
	var seatSkills, seatMCPs []string
	seatModel := ""
	if fc.Stores.StaffStore != nil {
		if row, ok := fc.Stores.StaffStore.Get(key, person); ok {
			seatSkills, seatMCPs = row.Skills, row.MCPs
			seatModel = row.Model
		}
	}
	skills := capability.EffectiveSkills(cfg.Skills, seatSkills)
	mcps := capability.EffectiveMCPs(cfg.MCPs, seatMCPs)
	fabric, err := capability.Compose(cfg,
		capability.ResolveSkills(fc.Stores.Library, skills),
		capability.ResolveMCPs(fc.Stores.Library, mcps),
		fc.Stores.Library.ModelAuthoring())
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// The seat's model pick replaces the composed tier — the same
	// fold the dispatcher's composeFor applies, so the preview IS what
	// the seat runs (the empty pick leaves the tier alone).
	if seatModel != "" {
		fabric.Model = &capability.ModelTier{ID: seatModel}
	}
	httputil.WriteJSON(w, map[string]any{
		"project": key,
		"person":  person,
		"skills":  skills,
		"mcps":    mcps,
		"fabric":  fabric,
	})
}

// --- composer slash commands -------------------------------------------

// chatCommand intercepts the host channel's say lines that start with
// /skill or /mcp (the composer's slash menu sends plain text — this
// is the whole「在对话中输入」management face). Returns true when the
// line was a command (executed, receipted as a system line + library
// broadcast; the line never enters the chat stream as a say).
//
// Grammar (first line = command, the lines after = the skill body):
//
//	/skill new <key> <名称>     [+ body lines]
//	/skill rm <key>
//	/skill list
//	/mcp add <key> <名称> <http|sse> <url>
//	/mcp rm <key>
//	/mcp list
//
// Header-carrying MCP servers stay behind the library editor/CLI — a
// one-line grammar has no honest place for secrets.
func (fc *Face) ChatCommand(h *chat.Hub, actor, text string) bool {
	if !strings.HasPrefix(text, "/skill ") && !strings.HasPrefix(text, "/mcp ") {
		return false
	}
	head, body, _ := strings.Cut(text, "\n")
	fields := strings.Fields(head)
	cmd := fields[0]
	switch {
	case cmd == "/skill" && len(fields) >= 2 && fields[1] == "list":
		fc.listCommand(h, actor, "skill")
	case cmd == "/skill" && len(fields) >= 4 && fields[1] == "new":
		fc.skillNewCommand(h, actor, fields[2], strings.Join(fields[3:], " "), strings.TrimSpace(body))
	case cmd == "/skill" && len(fields) >= 3 && fields[1] == "rm":
		fc.skillRmCommand(h, actor, fields[2])
	case cmd == "/mcp" && len(fields) >= 2 && fields[1] == "list":
		fc.listCommand(h, actor, "mcp")
	case cmd == "/mcp" && len(fields) >= 6 && fields[1] == "add":
		fc.mcpAddCommand(h, actor, fields[2], strings.Join(fields[3:len(fields)-2], " "), fields[len(fields)-2], fields[len(fields)-1])
	case cmd == "/mcp" && len(fields) >= 3 && fields[1] == "rm":
		fc.mcpRmCommand(h, actor, fields[2])
	default:
		h.SystemRecorded(i18n.Sf("命令形状不对：%s（用法：/skill new <key> <名称>＋换行正文 · /skill rm <key> · /skill list · /mcp add <key> <名称> <http|sse> <url> · /mcp rm <key> · /mcp list）", head))
	}
	return true
}

func (fc *Face) skillNewCommand(h *chat.Hub, actor, key, name, body string) {
	if fc.Stores.Library == nil {
		h.SystemRecorded(i18n.S("技能库不可用"))
		return
	}
	if !fc.skillSaveAllowed(actor) {
		h.SystemRecorded(i18n.Sf("技能库写入被拒：房主或 Lv.%d 及以上可写（%s 不满足）", verbs.KickMinRank, actor))
		return
	}
	sk := capability.Skill{Key: key, Name: name, Body: body, CreatedBy: actor}
	saved, err := fc.Stores.Library.UpsertSkill(sk)
	if err != nil {
		h.SystemRecorded(i18n.Sf("技能未保存：%v", err))
		return
	}
	h.SystemRecorded(i18n.Sf("已保存技能 %s（%s）——输入 /<key> 或在技能库装配给成员", saved.Key, saved.Name))
	h.Broadcast(chat.Message{Type: chat.MsgSkillEvt, Event: "saved",
		Skill: &chat.Skill{Key: saved.Key, Name: saved.Name,
			CreatedBy: saved.CreatedBy, CreatedTS: saved.CreatedTS},
		From: actor, Text: i18n.Sf("已保存技能 %s（%s，经对话命令）", saved.Key, saved.Name),
		TS: time.Now().Unix()})
}

func (fc *Face) skillRmCommand(h *chat.Hub, actor, key string) {
	if fc.Stores.Library == nil {
		h.SystemRecorded(i18n.S("技能库不可用"))
		return
	}
	if !fc.skillSaveAllowed(actor) {
		h.SystemRecorded(i18n.Sf("技能库写入被拒：房主或 Lv.%d 及以上可写（%s 不满足）", verbs.KickMinRank, actor))
		return
	}
	sk, ok := fc.Stores.Library.GetSkill(key)
	if !ok {
		h.SystemRecorded(i18n.Sf("技能 %s 不存在", key))
		return
	}
	refs := fc.skillReferences(key)
	if !fc.Stores.Library.RemoveSkill(key) {
		h.SystemRecorded(i18n.Sf("技能 %s 不存在", key))
		return
	}
	text := i18n.Sf("已删除技能 %s（%s）", key, sk.Name)
	if len(refs) > 0 {
		text = i18n.Sf("已删除技能 %s（%s）——仍在装配中，席位上已失效：%s", key, sk.Name, strings.Join(refs, "、"))
	}
	h.SystemRecorded(text)
	h.Broadcast(chat.Message{Type: chat.MsgSkillEvt, Event: "removed",
		Skill: &chat.Skill{Key: key, Name: sk.Name},
		From:  actor, Text: text, TS: time.Now().Unix()})
}

func (fc *Face) mcpAddCommand(h *chat.Hub, actor, key, name, typ, url string) {
	if fc.Stores.Library == nil {
		h.SystemRecorded(i18n.S("技能库不可用"))
		return
	}
	if !fc.skillSaveAllowed(actor) {
		h.SystemRecorded(i18n.Sf("技能库写入被拒：房主或 Lv.%d 及以上可写（%s 不满足）", verbs.KickMinRank, actor))
		return
	}
	saved, err := fc.Stores.Library.UpsertMCP(capability.MCPServer{
		Key: key, Name: name, Type: typ, URL: url, CreatedBy: actor,
	})
	if err != nil {
		h.SystemRecorded(i18n.Sf("MCP 服务未保存：%v（带请求头的服务请用技能库的 MCP 编辑器或 CLI）", err))
		return
	}
	h.SystemRecorded(i18n.Sf("已保存 MCP 服务 %s（%s，%s）——装配给成员后下次出生挂载", saved.Key, saved.Name, saved.URL))
	h.Broadcast(chat.Message{Type: chat.MsgMcpEvt, Event: "saved",
		Mcp:  mcpWireSummary(saved),
		From: actor, Text: i18n.Sf("已保存 MCP 服务 %s（%s，经对话命令）", saved.Key, saved.Name),
		TS: time.Now().Unix()})
}

func (fc *Face) mcpRmCommand(h *chat.Hub, actor, key string) {
	if fc.Stores.Library == nil {
		h.SystemRecorded(i18n.S("技能库不可用"))
		return
	}
	if !fc.skillSaveAllowed(actor) {
		h.SystemRecorded(i18n.Sf("技能库写入被拒：房主或 Lv.%d 及以上可写（%s 不满足）", verbs.KickMinRank, actor))
		return
	}
	m, ok := fc.Stores.Library.GetMCP(key)
	if !ok {
		h.SystemRecorded(i18n.Sf("MCP 服务 %s 不存在", key))
		return
	}
	refs := fc.mcpReferences(key)
	if !fc.Stores.Library.RemoveMCP(key) {
		h.SystemRecorded(i18n.Sf("MCP 服务 %s 不存在", key))
		return
	}
	text := i18n.Sf("已删除 MCP 服务 %s（%s）", key, m.Name)
	if len(refs) > 0 {
		text = i18n.Sf("已删除 MCP 服务 %s（%s）——仍在装配中，成员出生时不再挂载：%s", key, m.Name, strings.Join(refs, "、"))
	}
	h.SystemRecorded(text)
	h.Broadcast(chat.Message{Type: chat.MsgMcpEvt, Event: "removed",
		Mcp:  &chat.MCPServer{Key: key, Name: m.Name},
		From: actor, Text: text, TS: time.Now().Unix()})
}

func (fc *Face) listCommand(h *chat.Hub, actor, kind string) {
	if fc.Stores.Library == nil {
		h.SystemRecorded(i18n.S("技能库不可用"))
		return
	}
	if kind == "mcp" {
		ms := fc.Stores.Library.ListMCPs()
		if len(ms) == 0 {
			h.SystemRecorded(i18n.S("MCP 库为空——/mcp add <key> <名称> <http|sse> <url> 添加"))
			return
		}
		rows := make([]string, 0, len(ms))
		for _, m := range ms {
			rows = append(rows, i18n.Sf("%s（%s，%s %s）", m.Key, m.Name, m.Type, m.URL))
		}
		h.SystemRecorded(i18n.Sf("MCP 库 %d 个：%s", len(ms), strings.Join(rows, "；")))
		return
	}
	sks := fc.Stores.Library.ListSkills()
	if len(sks) == 0 {
		h.SystemRecorded(i18n.S("技能库为空——/skill new <key> <名称>＋换行正文 创建"))
		return
	}
	rows := make([]string, 0, len(sks))
	for _, sk := range sks {
		rows = append(rows, i18n.Sf("%s（%s）", sk.Key, sk.Name))
	}
	h.SystemRecorded(i18n.Sf("技能库 %d 个：%s——消息里以 /<key> 附着，或装配给成员", len(sks), strings.Join(rows, "；")))
}
