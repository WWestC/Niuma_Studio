package chat

import (
	"crypto/rand"
	"fmt"
)

// The room's client plumbing: the seat connection (send/done channels,
// the supersede close reason) and the seat-token minter. A client is a
// transport; every room decision stays with the Hub.

// Client is a room participant. It can be a remote WebSocket connection
// or the local UI. Consumers read from Receive; the Hub writes into it.
type Client struct {
	member Member
	send   chan Message
	done   chan struct{}

	// seatToken is this seat's ownership credential (seat-M1): handed
	// out at join, echoed in welcome, carried into the grace ghost on
	// Leave. Only a dial presenting the same token may supersede the
	// live seat or (matching) reclaim the ghost.
	seatToken string

	// closeReason is what the owning transport should use when done
	// closes (set BEFORE close(done); empty = the transport default,
	// which the WS layer renders as "removed from room").
	closeReason string
}

// CloseReason returns the transport close reason set with the seat's
// removal. Empty means "use the default" — a kicked member's reason
// stays the WS layer's historical string, byte-identical (t_12).
func (c *Client) CloseReason() string { return c.closeReason }

// Name returns the member name.
func (c *Client) Name() string { return c.member.Name }

// Member returns a copy of the member info.
func (c *Client) Member() Member { return c.member }

// Receive exposes the inbound message stream for this client.
func (c *Client) Receive() <-chan Message { return c.send }

// Done is closed when the client has been removed from the room.
func (c *Client) Done() <-chan struct{} { return c.done }

// trySend never blocks the Hub: a stalled consumer drops messages.
func (c *Client) trySend(msg Message) {
	select {
	case c.send <- msg:
	default:
	}
}

// newSeatToken mints a 128-bit random hex credential (seat-M1). The
// token is semantic integrity, not a security boundary (§2.3): any
// local process can read a token file, exactly as it can today use the
// name itself. crypto/rand failing is a broken host — fail hard
// （安全核查修复：时间戳回退是可预测凭证，全房间的座位都会共享一个
// 可猜的铸造；凭证从坏熵源里铸造宁可断，不可静默降级）.
func newSeatToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("seat token: crypto/rand 不可用，拒绝铸造可预测凭证：%v", err))
	}
	return fmt.Sprintf("%032x", b[:])
}
