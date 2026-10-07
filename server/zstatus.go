package server

import "net/http"

// The ZCode boot gate (GET /zcode): the app is bound to ZCode — the
// workbench's splash door waits on this face after the app is clicked,
// showing its detection animation until the bridge reports ready, the
// assistant's channel warm-up lands AND the flagship establishment is
// hired to headcount (see web/ui/boot.js). Failure keeps the door SHUT
// with the reason shown (no entering the office without ZCode); the door's 重新检测 rides this same poll, and main
// re-attempts a failed bridge at most once every 10s so a ZCode
// installed while waiting recovers by itself. Options.ZCodeStatus
// supplies the state; nil leaves the face absent (embeds/tests) and
// the splash's own miss-grace shows its failure panel instead of
// bricking the door.

// ZCodeState is the boot gate's one-shot answer.
type ZCodeState struct {
	// State is one of booting | warming | recruiting | ready | missing |
	// error | off:
	//   booting — the app-server child spawned, first protocol line
	//             still pending (the splash keeps waiting);
	//   warming — the child spoke and the 小助手's hidden warm-up turn
	//             is burning the model channel's cold-start window
	//             (the splash keeps waiting a capped beat so the host
	//             enters to a warm channel; a failed or expired
	//             warm-up opens the door all the same);
	//   recruiting — the bridge is live and the flagship establishment
	//             is being hired to headcount (编排者/HR): the splash
	//             shows the hiring progress and the door holds until
	//             every mandatory post is seated — the office never
	//             opens understaffed;
	//   ready   — the warm-up is done or skipped AND the establishment
	//             is filled (the splash enters the office);
	//   missing — the ZCode CLI bundle is not installed (auto-retried,
	//             installs mid-wait are picked up — door stays shut);
	//   error   — the child failed to start, died, or never responded;
	//   off     — dispatch disabled by --no-dispatch (an operator's
	//             explicit opt-out: the door opens with a note).
	State string `json:"state"`
	// Detail carries the human line for the failure panel (the bundle
	// path for missing, the error text otherwise) or the recruiting
	// hold's current hire ("小马（HR）"; a refused hire appends the
	// reason — the door waits and the splash says why), or the
	// warming hold's elapsed count ("正在预热模型通道（已等待 n 秒）"
	// — the splash can show the wait is moving, not wedged).
	Detail string `json:"detail,omitempty"`
	// Recruit rides State=="recruiting": the establishment hiring
	// progress for the splash's "已到岗 n/N" counter. Nil elsewhere.
	Recruit *RecruitView `json:"recruit,omitempty"`
}

// RecruitView is the boot gate's hiring counter (dispatch's
// RecruitProgress mapped — the server stays dispatch-free; main wires
// the projection).
type RecruitView struct {
	Filled  int    `json:"filled"`
	Total   int    `json:"total"`
	Current string `json:"current,omitempty"`
	Blocked string `json:"blocked,omitempty"`
}

// handleZCode answers GET /zcode with the current bridge state.
func (s *Server) handleZCode(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/zcode" || s.opts.Faces.ZCodeStatus == nil {
		http.NotFound(w, r)
		return
	}
	st := s.opts.Faces.ZCodeStatus()
	if st.State == "" {
		st.State = "booting"
	}
	writeJSON(w, st)
}
