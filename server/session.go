package server

import (
	"context"
	"github.com/WWestC/Niuma_Studio/server/auth"
	"log"
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/wire"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// The WS session loop: the hello handshake (join dedup, seat tokens,
// the owner-mirror seatless path), the reader's verb dispatch, the
// writer pump and the 25s heartbeat. One connection, one session, one
// room; the room's decisions are the hub's.

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// The library's 32KB default read limit sits BELOW a full-cap kb
	// write frame (32KB body + JSON envelope), so a legal最大 body was
	// rejected by transport before the engine ever saw it (t_82's V6e).
	// 128KB comfortably covers the 64KB body cap (v0.6 §3.3) plus the
	// JSON envelope for every frame.
	conn.SetReadLimit(128 * 1024)
	ctx := r.Context()

	// First frame must be hello{name, role}. replay defaults to true;
	// {"replay":false} opts out of history. observer (v2 P1) marks a
	// seatless read-only dial — the Web workbench window. project (v2
	// P1/P3-a) names the room; absent means the default room.
	var hello struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Role   string `json:"role"`
		Replay *bool  `json:"replay"`
		// Token is the seat credential (seat-M1): only persistent
		// clients (guard) present one — presenting the token this
		// seat holds takes the live seat over (supersede, no "-2").
		Token string `json:"token,omitempty"`
		// Force opts INTO the dedup twin (the CLI's --force-seat) —
		// the seat door below refuses an unforced mismatched dial.
		Force bool `json:"force,omitempty"`
		// Observer + Project are the additive v2 P1 hello fields
		// (chat/wire.go documents the wire contract).
		Observer bool   `json:"observer,omitempty"`
		Project  string `json:"project,omitempty"`
		// Visitor is the r_19 share token — ONLY token-carrying observer
		// dials are visitors (the /view page); the host's own workbench
		// windows leave it absent and never enter 访客治理.
		Visitor string `json:"visitor,omitempty"`
		// Auth is the login session token (multi-user shape): the dial's
		// identity proof — a Bearer-grade credential, constant-time
		// checked, without which a multi-user dial is refused at the
		// door. Absent under single-user (zero friction unchanged) and
		// for loopback operator dials.
		Auth string `json:"auth,omitempty"`
		// Proto is the client's protocol version (version negotiation,
		// additive-only): 0/absent = a pre-negotiation client (served at
		// the current version); a value ABOVE the server's refuses the
		// dial — a newer client speaking to an older server must not
		// assume frames the server never promised.
		Proto int `json:"proto,omitempty"`
	}
	if err := wsjson.Read(ctx, conn, &hello); err != nil || hello.Type != chat.MsgHello {
		_ = conn.Close(websocket.StatusPolicyViolation,
			`first message must be {"type":"hello","name":"..."}`)
		return
	}
	replay := true
	if hello.Replay != nil {
		replay = *hello.Replay
	}
	// 协议版本协商（additive-only 的执行点）：客户端报自己的最高版
	// 本，服务端版本以上者拒之——老客户端（0/缺席）按当前版本照常服
	// 务。welcome 帧回执服务端版本，客户端据此知悉协商结果。
	if hello.Proto > wire.ProtoVersion {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = wsjson.Write(wctx, conn, chat.Message{Type: chat.MsgSystem,
			Text: i18n.Sf("客户端协议版本 %d 高于服务端 %d——请先升级服务端程序", hello.Proto, wire.ProtoVersion),
			TS:   time.Now().Unix()})
		cancel()
		_ = conn.Close(websocket.StatusPolicyViolation, "proto too new")
		return
	}

	// v2 P3-a room routing (项目=办公室): an explicit hello.project
	// resolves through the registry — every later frame of this
	// connection belongs to that room only. An absent or "default"
	// project stays on the server's own hub (the lobby), so every
	// legacy dial keeps its exact pre-P3 behavior. Unknown, draft
	// (never opened) and archived (frozen) projects are refused before
	// any seat logic.
	hub := s.hub
	if s.opts.Registry != nil && hello.Project != "" && hello.Project != chat.LobbyKey {
		ph, rerr := s.opts.Registry.Hub(hello.Project)
		if rerr != nil {
			rejectProject(ctx, conn, hello.Project, rerr)
			return
		}
		hub = ph
	}

	// 身份解析（多用户形态）：hello.auth 或握手 cookie 换会话；回环
	// 拨号自动以操作者入座（机器信任边界的显式化）。访客通道
	// （带有效 token 的 observer 拨号）自带头衔——r_19 的独立 token
	// 语义原样保留。无身份的多用户拨号在此拒之：「连接即身份」的
	// 废除点。
	visitorOK := hello.Visitor != "" && s.visitors != nil && s.visitor.Check(hello.Visitor)
	if s.authRequired(hello.Visitor, visitorOK) && s.dialAuth(r, hello.Auth) == nil {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = wsjson.Write(wctx, conn, map[string]any{
			"type":   "error",
			"text":   i18n.S("本工作室已启用多用户——请先登录获取会话令牌（POST /auth/login），hello 携带 auth 字段或 cookie"),
			"status": http.StatusUnauthorized,
		})
		cancel()
		_ = conn.Close(websocket.StatusPolicyViolation, "auth required")
		return
	}

	// Observer dial (v2 P1, frontend 稿 §4.6-1): seatless, read-only,
	// receives every broadcast. Checked before any name logic — an
	// observer holds no identity, so its name (if any) is ignored and
	// can never collide with or shadow a live member.
	if hello.Observer {
		// r_19 访客治理（t_191）·修订：治理只认带 token 的拨号（/view
		// 分享页）——限流（并发帽＋重连窗）、4h TTL、彩蛋全部落在访客
		// 头上；房主自己的工作台窗口不带 token，永不进访客计数。修订
		// 前的 bug：所有 observer 拨号无差别进限流，通道关着也计数——
		// 多开几个窗口（应用/预览页/代理）就把每房 5 个名额占满，房主
		// 自己反被「当前访客较多」拒之门外，窗口永远停在「仅缓存 ·
		// HTTP 仍活」。死 token 走 404（与 /view 同纪律——不确认存在
		// 性），文案给访客指路（4h TTL 到点的重连同此路）。
		if hello.Project == "" {
			hello.Project = chat.LobbyKey // 计数键归一：省略 project 的拨号与显式 default 同键（serveObserver 同款归一，👁 角标才看得见）
		}
		if s.visitors != nil && hello.Visitor != "" {
			if !s.visitor.Check(hello.Visitor) {
				_ = wsjson.Write(ctx, conn, map[string]any{
					"type":   "error",
					"text":   i18n.S("访客链接已失效，请向房主索取新链接"),
					"status": http.StatusNotFound,
				})
				return
			}
			if ok, status, line := s.visitors.Admit(hello.Project); !ok {
				_ = wsjson.Write(ctx, conn, map[string]any{
					"type": "error", "text": line, "status": status,
				})
				return
			}
			defer s.visitors.Release(hello.Project)
			s.visitorFirstOnce(hello.Project)
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, auth.VisitorMaxTTL)
			defer cancel()
		}
		s.serveObserver(ctx, conn, replay, hello.Project, hub)
		return
	}

	// v0.6 M1 mirror path, generalized to every room (v2 P7 lobby
	// parity): a hello under the owner's name while the owner's own
	// seat already holds it IN THAT ROOM is the owner write face
	// dialing (the CLI mirror hook, the workbench's OwnerChannel). A
	// normal join would land on "owner-2" — a phantom seat the room
	// treats as an intruder — so this connection goes seatless instead:
	// it speaks through the owner's real seat and leaves no trace (no
	// join, no leave, no seat). With the owner seat free, the normal
	// join below lands on the owner's name unchanged. The registry
	// pre-seats the owner in every project room at instantiation, so
	// project dials take this path exactly like lobby dials; a room
	// without a live owner seat (a bare embed) keeps the seated join.
	// owner-M1: the hello's seat token rides along — the attribution-
	// bearing verbs (say / whisper / ack / react, any origin) verify it
	// frame-by-frame against the live owner seat's credential. The name
	// alone stopped being an identity here after the p/book incident
	// (an agent ran the documented mirror CLI and spoke AS the host):
	// management frames keep the loopback trust root, but nothing on
	// this channel attaches the owner's name to a room line without
	// proof.
	if s.opts.LocalName != "" && hello.Name == s.opts.LocalName && hub.HasLiveMember(s.opts.LocalName) {
		s.serveMirror(ctx, conn, hub, hello.Token)
		return
	}

	// The seat door (the "-2" twin's last open path): a hello that
	// joinLocked would dedup-rename into a phantom twin — join broadcast
	// + rename broadcast + a grace ghost + a second body on the map.
	// SeatDialTwins is joinLocked's own collision rule, ghosts included:
	// a LIVE holder under a different credential (the 小鸟-2 incident:
	// a member's own session hand-rolled a raw WS dial under its name
	// while the dispatcher held the seat), and — the 小苗-2 incident —
	// a GRACE ghost under a mismatched credential: the dialer presents
	// the token its CURRENT room's dispatcher stamped, the ghost carries
	// the credential of a room the member left (snapshot-restored, and
	// a paused room freezes the grace clock forever). Every CLI face
	// already refuses before dialing (say / listen / wait / task probes,
	// guard supersedes by token) and the dispatcher rides JoinFree's
	// refusal — the door covers what still arrives bare: hand-rolled
	// clients and wrong-room dials. The exceptions are exactly the
	// member's own paths: a same-token dial supersedes (the one-shot
	// write channel), a freed or grace seat still reclaims by name (a
	// bare dial), and an explicit force dial (hello.force, the CLI's
	// --force-seat) keeps the documented twin path. Names are unique
	// identities in this house (roster/rank/task bind the exact name) —
	// a second same-name dial was never a legitimate join, only an
	// accident waiting to ghost.
	if twins, graceHeld := hub.SeatDialTwins(hello.Name, hello.Token); twins && !hello.Force {
		text := i18n.Sf("「%s」的座位由在线连接持有（凭据不匹配）——已拒绝拨号，未产生分身。同座重连请携带座位 token（CLI 命令自动携带）；确要分身请显式带 force", hello.Name)
		if graceHeld {
			text = i18n.Sf("「%s」的座位在宽限占用中（携带凭据与座位不匹配）——已拒绝拨号，未产生分身。座位凭据只在本人房间有效：若你在别的房间上班，请到自己的房间发言（CLI 缺省按工作区绑定房拨号）；确要收回此房宽限座请免凭据拨入；确要分身请显式带 force", hello.Name)
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = wsjson.Write(wctx, conn, chat.Message{
			Type: chat.MsgSystem,
			Text: text,
			TS:   time.Now().Unix()})
		cancel()
		_ = conn.Close(websocket.StatusPolicyViolation, "seat held")
		return
	}

	dialRole := s.dialAuth(r, hello.Auth)

	// Join queues welcome (+history) + join into the client stream, so
	// the wire order is correct without any direct writes here. The
	// explicit hello.project (if any) rides the welcome back; an absent
	// one keeps the legacy welcome byte-identical.
	client, member := hub.JoinProject(hello.Name, hello.Role, replay, hello.Token, hello.Project)
	defer hub.Leave(client)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-client.Done():
				// the seat is gone (kick or same-token supersede): drop
				// the transport so the read loop below unblocks; the
				// deferred Leave is idempotent. The hub sets a specific
				// reason for supersedes; everything else keeps the
				// historical kick string, byte-identical (t_12).
				reason := client.CloseReason()
				if reason == "" {
					reason = "removed from room"
				}
				_ = conn.Close(websocket.StatusPolicyViolation, reason)
				return
			case m, ok := <-client.Receive():
				if !ok {
					return
				}
				wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := wsjson.Write(wctx, conn, m)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	log.Printf("member joined: %s", member.Name)

	// t_09 heartbeat: a client that stops ponging (dead or wedged
	// process behind a half-open TCP) fails conn.Ping; closing the
	// transport unblocks the read loop below into the normal abnormal-
	// drop path (Leave → presence grace), so no new teardown logic is
	// needed. Ping/CloseNow are goroutine-safe on coder/websocket.
	if s.opts.Endpoint.Heartbeat > 0 {
		go func() {
			ticker := time.NewTicker(s.opts.Endpoint.Heartbeat)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-client.Done():
					return
				case <-writerDone:
					return
				case <-ticker.C:
					pctx, cancel := context.WithTimeout(context.Background(), s.opts.Endpoint.Heartbeat)
					err := conn.Ping(pctx)
					cancel()
					if err != nil {
						// CloseNow, not Close: a graceful close waits
						// for the peer's close-frame echo, and a client
						// that stopped ponging will never send one —
						// the 5s handshake wait would keep the read
						// loop (and the seat) alive exactly when we
						// are trying to prove it dead.
						_ = conn.CloseNow()
						return
					}
				}
			}
		}()
	}
	for {
		// 每帧全新信封：encoding/json 对非 nil 指针字段原地在解码
		// （下面的 *Patch/*Task/*Doc/*Pack/*Plan 都是可选对象），
		// 复用循环外变量会把上一帧的补丁字段漏进下一帧——长连接
		// 上连续多个 task_update 就串成一串合并补丁。零值重来。
		var in verbs.Envelope
		if err := wsjson.Read(ctx, conn, &in); err != nil {
			break
		}
		if handle, ok := verbs.Table[in.Type]; ok {
			// 角色门（多用户形态）：动词的成文最低角色逐帧裁决——会话
			// 身份不足者私拒（房间听得见动静，拨号者收得到缘由）。单
			// 用户（无身份服务）恒放行：历史行为逐字节不变。
			if min, gated := verbRoleFloor(in.Type); gated && s.multiuser() {
				if !dialRole.AtLeast(min) {
					denyVerbRole(hub, client, in.Type, dialRole, min)
					continue
				}
			}
			handle(s.seat, hub, client, &member, &in)
		} else if in.Type == chat.MsgBye {
			// hand the seat to the presence grace BEFORE the close
			// frame goes out: the byeing client (and the next same-name
			// run it is about to become) must never observe the seat as
			// still live, or the rejoin dedups into a "-2" rename —
			// which silently swaps the agent's cursor file and
			// resurrects already-delivered history (t_37 mode 2).
			// Leave is idempotent; the deferred call is a no-op.
			hub.Leave(client)
			_ = conn.Close(websocket.StatusNormalClosure, "")
			select {
			case <-writerDone:
			case <-time.After(time.Second):
			}
			log.Printf("member left: %s", member.Name)
			return
		}
	}
	// Abnormal drop (read error = dead transport): same ordering as the
	// Bye branch — release the seat before teardown so a same-name
	// reconnect never races the deferred Leave into a "-2" dedup.
	hub.Leave(client)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	select {
	case <-writerDone:
	case <-time.After(time.Second):
	}
	log.Printf("member left: %s", member.Name)
}

// engineOp runs op against the task engine; without one configured the
// operation is denied (main always passes an engine — this guards
// embedders and tests).
