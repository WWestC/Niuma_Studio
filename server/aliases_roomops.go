package server

// aliases_roomops.go — the room-lifecycle domain's re-export shim
// (the domain moved to server/roomops; these aliases keep the shell's
// tests and references unchanged — same discipline as FleetAPI).

import "github.com/WWestC/Niuma_Studio/server/roomops"

// The pre-wire management verbs' frame constants (kick.go/rank.go/
// archive.go history): the vocabulary lives in the domain now.
const (
	MsgKick         = roomops.MsgKick
	MsgRankSet      = roomops.MsgRankSet
	MsgAgentArchive = roomops.MsgAgentArchive
)

// The keeper/watch surface (main wires it at boot).
type KeeperConfig = roomops.KeeperConfig

var (
	StartKeepers      = roomops.StartKeepers
	WatchEveryFromEnv = roomops.WatchEveryFromEnv
)

// MemberRebuild is the rebuild vote's fire leg (FleetAPI's RebuildFire
// adapter in main dials it).
func (s *Server) MemberRebuild(project, by, tally string) error {
	return s.roomopsFace().MemberRebuild(project, by, tally)
}

type Keepers = roomops.Keepers
type OffboardResponse = roomops.OffboardResponse
type OffboardStep = roomops.OffboardStep
