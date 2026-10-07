package server

// aliases_autopilot.go — the 全智能模式 domain's re-export shim (the
// engine moved to server/autopilot; Options wiring, boot's env
// resolvers and the tests keep their names).

import (
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server/autopilot"
)

type (
	AutoPilotConfig = autopilot.Config
	AutoPilotEngine = autopilot.Engine
)

const (
	AutoPilotActor    = autopilot.AutoPilotActor
	AutoPilotMaxPlans = autopilot.AutoPilotMaxPlans
	AutoPilotEvery    = autopilot.AutoPilotEvery
)

var (
	AutoPilotEveryFromEnv                     = autopilot.AutoPilotEveryFromEnv
	AutoPilotAcceptDelayFromEnv               = autopilot.AutoPilotAcceptDelayFromEnv
	AutoPilotPokeEveryFromEnv                 = autopilot.AutoPilotPokeEveryFromEnv
	AutoPilotMaxPlansFromEnv                  = autopilot.AutoPilotMaxPlansFromEnv
	AutoPilotStallDelayFromEnv                = autopilot.AutoPilotStallDelayFromEnv
	AutoPilotAcceptDelay        time.Duration = autopilot.AutoPilotAcceptDelay
	AutoPilotPokeEvery          time.Duration = autopilot.AutoPilotPokeEvery
	AutoPilotStallDelay         time.Duration = autopilot.AutoPilotStallDelay
)

// startAutoPilot boots the engine over the cached face — the boot leg
// (Start) and the tests share it (the shell's pre-split shape).
func (s *Server) startAutoPilot(cfg AutoPilotConfig) *AutoPilotEngine {
	s.autopilot = autopilot.NewEngine(cfg, s.autopilotFace())
	return s.autopilot
}

// autoPilotDoneAs delegates the wrap-up verb to the domain face (the
// tests drive it directly).
func (s *Server) autoPilotDoneAs(hub *chat.Hub, actor, note, projectKey string) chat.Message {
	return s.autopilotFace().AutoPilotDoneAs(hub, actor, note, projectKey)
}
