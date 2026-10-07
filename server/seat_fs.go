package server

// seat_fs.go — the file-browsing face's mount point: the fsview.Face
// constructor (the family-split pattern: the shell owns routes and
// role floors, the face package owns the decisions).

import (
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server/autopilot"
	"github.com/WWestC/Niuma_Studio/server/fsview"
	"github.com/WWestC/Niuma_Studio/server/fswatch"
	"github.com/WWestC/Niuma_Studio/server/gitops"
	"github.com/WWestC/Niuma_Studio/server/plans"
	"github.com/WWestC/Niuma_Studio/server/roomops"
	"github.com/WWestC/Niuma_Studio/server/skills"
	"github.com/WWestC/Niuma_Studio/util"
)

func (s *Server) fsFace() *fsview.Face {
	s.ensureSeat()
	return &fsview.Face{Projects: s.opts.Stores.ProjectStore}
}

// startFsWatch brings up the workspace watcher (文件板自动刷新的服务端
// 半边): every registered project's workspace gets the poll-signature
// loop, a changed project lands one fs_dirty frame on every room's hub.
// Nil ProjectStore keeps it off (embeds, tests).
func (s *Server) startFsWatch() {
	if s.opts.Stores.ProjectStore == nil {
		return
	}
	s.fsWatch = fswatch.NewWatcher(s.opts.Stores.ProjectStore, s.fsDirtyBroadcast)
	go s.fsWatch.Loop()
}

// fsDirtyBroadcast is the watcher's Notify 落点：fs_dirty 帧进每间开着
// 门的房。观察者按房常开（切房不断流），正浏览某项目工作区的窗口可能
// 坐在任何一间房——全员广而告之、前端按 frame.project 自行取舍，是
// rebuild 全房通告同款拓扑；帧不进历史不 badge，无客户端的空房零成本。
func (s *Server) fsDirtyBroadcast(project string) {
	frame := chat.Message{Type: chat.MsgFsDirty, Project: project, TS: util.Now()}
	if s.opts.Registry != nil {
		for _, h := range s.opts.Registry.Rooms() {
			if h != nil {
				h.Broadcast(frame)
			}
		}
	}
	if s.hub != nil {
		s.hub.Broadcast(frame) // 大厅——registry 从不收编 lobby key，根 hub 单独走
	}
}

// skillsFace is the capability-assembly domain's mount point (the
// Face adapts the seat — domain verbs and HTTP mounts drink the same).
func (s *Server) skillsFace() *skills.Face {
	s.ensureSeat()
	return skills.FaceOf(s.seat)
}

// gitFace is the git domain's mount point (Face adapts the seat plus
// the chronicle/plan-hub seams).
func (s *Server) gitFace() *gitops.Face {
	s.ensureSeat()
	if s.gitFaceVal == nil {
		s.gitFaceVal = gitops.FaceOf(s.seat, s.chronicle, s.planHub, s.opts.GitWorktreeRoot)
	}
	return s.gitFaceVal
}

// roomopsFace is the room-lifecycle domain's mount point (the Face
// adapts the seat plus the replay/chronicle/plan-hub/snapshot/git-boot
// seams the shell owns).
func (s *Server) roomopsFace() *roomops.Face {
	s.ensureSeat()
	if s.roomopsFaceVal != nil {
		return s.roomopsFaceVal
	}
	f := roomops.FaceOf(s.seat, s.replays, s.planHub)
	f.Chronicle = s.chronicle
	f.SnapshotTo = s.writeSnapshotTo
	f.GitBootstrap = s.gitFace().BootstrapProjectGit
	f.RebuildCh = s.opts.RebuildCh
	f.ResetCh = s.opts.ResetCh
	s.roomopsFaceVal = f
	return f
}

// plansFace is the plan/requirement/meeting review domain's mount
// point (chronicle and plan-hub seams ride along).
func (s *Server) plansFace() *plans.Face {
	s.ensureSeat()
	f := plans.FaceOf(s.seat, s.chronicle, s.planHub)
	f.ObserveAchieve = func(hub interface{ System(string) }, ev plans.Event) {
		s.observeAchievements(hub, ev)
	}
	f.WireNightTap = s.wireNightTap
	return f
}

// autopilotFace is the 全智能模式 domain's mount point (git/plans
// sibling faces and the spend seam ride along). Cached: NewEngine
// writes the engine's back-reference into THIS instance — a fresh one
// per call would leave the write face reading a nil engine.
func (s *Server) autopilotFace() *autopilot.Face {
	s.ensureSeat()
	if s.autopilotFaceVal == nil {
		s.autopilotFaceVal = &autopilot.Face{
			Seat:    s.seat,
			PlanHub: s.planHub,
			Git:     s.gitFace(),
			Plans:   s.plansFace(),
			Spend:   s.spend.get,
		}
	}
	return s.autopilotFaceVal
}
