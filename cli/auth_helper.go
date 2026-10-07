package cli

// auth_helper.go — the identity layer's CLI leg: under multi-user the
// dial must prove itself. Local CLI tooling proves with the studio
// service token (boot-minted at ~/.niuma_service_token, 0600 — the
// machine's own credential); absent file = single-user shape, dial
// bare as always. The hello also carries the protocol version for
// negotiation (a too-new CLI against an older server refuses loudly
// instead of speaking unfulfilled promises).

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/wire"
)

// loadServiceToken reads the machine's studio credential ("" when the
// file is absent — single-user, or a multi-user studio not on this
// machine).
func loadServiceToken() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".niuma_service_token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// authHello decorates one hello with the identity layer's fields:
// proto always (negotiation), auth when a credential is at hand.
func authHello(hello *chat.Message) {
	hello.Proto = wire.ProtoVersion
	if tok := authToken(); tok != "" {
		hello.Auth = tok
	}
}

// authToken answers the dial's credential, in priority order:
// NIUMA_AUTH_TOKEN (an explicit login session token — the remote
// member's carrier: the operator mints a member account, hands its
// session token to the remote machine, the CLI there presents it and
// gets exactly member-grade authority), then the machine's service
// token file (the local zero-friction path).
func authToken() string {
	if v := strings.TrimSpace(util.Env("AUTH_TOKEN")); v != "" {
		return v
	}
	return loadServiceToken()
}
