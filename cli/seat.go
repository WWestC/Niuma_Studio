package cli

// Seat-credential plumbing for guard (seat-M1, v0.10 t_82): the token
// the server handed out at the last successful dial is persisted next
// to the wait cursor (~/.niuma_token_<name>, 0600) and
// presented on every reconnect — presenting it is what takes the
// member's own live seat back (supersede) instead of deduping to a
// "-2" twin. Losing the file loses nothing but the supersede ability:
// the grace ghost still reclaims by name.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// seatTokenPath returns ~/.niuma_token_<name> — chat.SeatTokenPath in
// cli clothing (the helpers live in chat now so the dispatcher, which
// holds member seats in-process, persists to the exact files the CLI
// reads).
func seatTokenPath(name string) string { return chat.SeatTokenPath(name) }

// loadSeatToken reads the saved credential ("" when absent/unreadable —
// a first dial, or a reset session). The key is PROJECT-scoped when the
// CLI's cwd is bound to a project（分项目凭证键——流转房的凭证互不覆
// 盖），with the legacy name-only file as the read fallback（the owner's
// own credential and pre-re-keying seats live there）.
func loadSeatToken(name string) string { return chat.LoadSeatToken(scopeProject(), name) }

// saveSeatToken persists the credential atomically (temp+rename) with
// 0600 — owner-only, like the wait cursor. Same project-scoped key as
// loadSeatToken（the dispatcher stamps the same pair on every attach）.
func saveSeatToken(name, token string) { chat.SaveSeatToken(scopeProject(), name, token) }

// wrongRoomDial is the 错房守卫（分项目凭证键的反面）：the caller has NO
// credential for the room it is about to dial, yet the name holds one in
// ANOTHER project room — dialing bare would reclaim a grace ghost by
// name（exactly the 串房 the split exists to end; the 小苗-2 shape,
// restated as the new key's own rule）. nil = dial on（the room is the
// member's own, or a genuine first dial with no credential anywhere）.
func wrongRoomDial(name, project string) error {
	room := project
	if room == "" {
		room = chat.LobbyKey
	}
	others := chat.SeatTokenProjects(name)
	for _, p := range others {
		if p == room {
			return nil
		}
	}
	if len(others) == 0 {
		return nil
	}
	return errors.New(i18n.Sf("%s 的座位凭证属于项目房 %v，不在你要拨的 %s——请 cd 进该项目工作区（或设 NIUMA_PROJECT / 显式 --project）再拨，勿错房裸拨",
		name, others, room))
}

// dialSeat is dial with a seat credential (seat-M1): presents token on
// hello (empty = legacy first dial) and returns the token the welcome
// carried — the seat's current credential, worth persisting. project
// rides hello.project so the resident seat sits in the member's own
// office ("" keeps the legacy lobby dial).
func dialSeat(ctx context.Context, url, name, role string, replay bool, token, project string) (*websocket.Conn, chat.Member, string, error) {
	if token == "" {
		if err := wrongRoomDial(name, project); err != nil {
			return nil, chat.Member{}, "", err
		}
	}
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, chat.Member{}, "", err
	}
	hello := chat.Message{Type: chat.MsgHello, Name: name, Role: role, Replay: &replay, Project: project}
	if token != "" {
		hello.Token = token
	}
	authHello(&hello)
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		_ = conn.CloseNow()
		return nil, chat.Member{}, "", err
	}
	var welcome chat.Message
	if err := wsjson.Read(ctx, conn, &welcome); err != nil {
		_ = conn.CloseNow()
		return nil, chat.Member{}, "", err
	}
	if welcome.Type != chat.MsgWelcome || welcome.You == nil {
		_ = conn.CloseNow()
		return nil, chat.Member{}, "", fmt.Errorf("unexpected handshake reply: %+v", welcome)
	}
	return conn, *welcome.You, welcome.Token, nil
}

// yieldWindow is the anti-ping-pong span (seat-M1 §3.1, default 10s):
// being superseded twice within it means another guard of the same
// name holds the same token file — one of the two must stand down or
// they will swap the seat forever.
const yieldWindow = 10 * time.Second

// shouldYield reports whether superseded timestamps show a ping-pong
// (two supersedes inside the yield window ending at now).
func shouldYield(superseded []time.Time, now time.Time) bool {
	recent := 0
	for _, ts := range superseded {
		if now.Sub(ts) <= yieldWindow {
			recent++
		}
	}
	return recent >= 2
}
