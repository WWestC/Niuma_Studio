package server

// session_observer.go — the observer faces of the WebSocket session
// layer, split out of session.go (which keeps the seated-member loop):
// the project-refusal receipt, the seatless read-only observer pump
// (the workbench window's connection), and the write-back helper.

import (
	"context"
	"github.com/WWestC/Niuma_Studio/server/auth"
	"log"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// rejectProject refuses a dial that named an unusable room (v2 P3-a):
// one system frame carrying the reason — a dialing AI reads frames,
// not close codes — then a policy close. Short ASCII close reason: the
// websocket close payload caps at 123 bytes and the detail is Chinese.
func rejectProject(ctx context.Context, conn *websocket.Conn, key string, err error) {
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = wsjson.Write(wctx, conn, chat.Message{
		Type: chat.MsgSystem,
		Text: i18n.Sf("项目 %s 不可用：%v", key, err),
		TS:   time.Now().Unix(),
	})
	cancel()
	_ = conn.Close(websocket.StatusPolicyViolation, "project unavailable")
}

// serveObserver runs the seatless read-only connection (v2 P1): no
// Join, no seat, no roster entry, no join/leave broadcast — the hub's
// AttachObserver handshake echo (roster + project, no "you"), then the
// observer's writer pump and heartbeat mirror the seated session loop,
// because an observer is long-lived (a browser workbench window) and
// receives every broadcast. hub is the room the hello routed to (P3-a:
// an explicit project observes THAT room; the echo's project field
// defaults to "default"). The read side accepts nothing but bye: any
// write verb draws a private system nudge so misuse is visible, never
// silent (the owner-management write channel is a separate design,
// §6-1 of the frontend 稿 — not this connection). The deferred Leave
// detaches the observer without a trace.
func (s *Server) serveObserver(ctx context.Context, conn *websocket.Conn, replay bool, project string, hub *chat.Hub) {
	if project == "" {
		project = "default"
	}
	client := hub.AttachObserver(project, replay, auth.VisitorReplay)
	defer hub.Leave(client)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-client.Done():
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

	log.Printf("observer connection opened (project %s)", project)

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
					// App-level keepalive ahead of the protocol ping: the
					// workbench's inbound watchdog (conn.js) can't see
					// ping/pong control frames, so before this a quiet
					// room force-reconnected every 60s and each trip
					// rendered a 断线补窗 divider. Rides the writer pump
					// via SendTo (one-writer rule); no seq, no history,
					// this observer only.
					hub.SendTo(client, chat.Message{Type: chat.MsgPing, TS: time.Now().Unix()})
					pctx, cancel := context.WithTimeout(context.Background(), s.opts.Endpoint.Heartbeat)
					err := conn.Ping(pctx)
					cancel()
					if err != nil {
						_ = conn.CloseNow()
						return
					}
				}
			}
		}()
	}

	var in struct {
		Type string `json:"type"`
	}
	for {
		if err := wsjson.Read(ctx, conn, &in); err != nil {
			break
		}
		switch in.Type {
		case chat.MsgBye:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			select {
			case <-writerDone:
			case <-time.After(time.Second):
			}
			log.Printf("observer connection closed (project %s)", project)
			return
		default:
			// observers never see the broadcast echo of their own writes
			// because they cannot write — the nudge goes straight back.
			writeBack(ctx, conn, chat.Message{
				Type: chat.MsgSystem,
				Text: i18n.S("观察者连接只读：发言与管理操作请走成员或房主通道"),
				TS:   time.Now().Unix(),
			})
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	select {
	case <-writerDone:
	case <-time.After(time.Second):
	}
	log.Printf("observer connection closed (project %s)", project)
}

// serveMirror runs the seatless owner connection (v0.6 M1, extended by
// t_05/t_10/t_11, generalized to project rooms by v2 P7): no Join, no
// seat, no leave. hub is the room the hello routed to — the owner's
// seat there (registry-pre-seated in project rooms) carries the
// identity and speaks the says, while the roster-management verbs
// (kick / rank_set / agent_archive) stay anchored to the LOBBY roster
// exactly as before. The handshake echo, then the frames the owner's
// name may send without holding a seat —
// {"type":"say","origin":"mirror"} lines spoken as the owner's real
// seat (each echoed back so the relay CLI can verify the origin marker
// survived) and the management frames, executed as the owner with the
// receipt written straight back, plus kb_write / kb_append under the
// owner's name (t_39 — the owner's CLI kb path; conditional-write
// semantics intact). The owner's seat is always live here (the router
// checked), so a seated join would dedup into "owner-2" — this channel
// is the only way an owner-identity frame reaches the room. Any other
// frame draws a private system nudge so misuse is visible, never
// silent. The connection relies on the client closing it (the CLI
// sends bye right after its echo); a dead peer unblocks the read the
// usual transport way.
//
// owner-M1: presented is the hello's seat token. The attribution-
// bearing verbs (say / whisper / ack / react — any origin) require it
// to match the live owner seat's credential, checked once here in
// constant time: the name on this channel is a claim, the token is the
// proof (the p/book incident — an agent speaking AS the host through
// the documented mirror CLI — is exactly the shape this kills). The
// management verbs stay name-acted: the loopback trust root this app
// runs on (every HTTP admin endpoint is equally local) is unchanged.
// writeBack sends one kb receipt frame to the seatless connection.
func writeBack(ctx context.Context, conn *websocket.Conn, m chat.Message) {
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = wsjson.Write(wctx, conn, m)
	cancel()
}
