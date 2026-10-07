package dispatch

// sharedBridge (v2 P3-b): the Fleet runs one zcode app-server child
// but N per-project dispatchers, and a raw Bridge's SetHooks/SetReverse
// REPLACE — the last Start would silently eat every other room's turn
// events and reverse requests. This decorator turns those two slots
// into fan-out registries instead: session/state events reach every
// dispatcher (each no-ops on sessions it does not own — lookup by
// session id), and a reverse request is answered exactly once, by the
// OWNING dispatcher when there is one (an unknown session falls to the
// first registrant — the fleet shares one process policy). All other
// Bridge methods pass straight through. Dispatcher.Start detects the
// decorator and registers through add; a raw bridge keeps the v1 path.
//
// Non-dispatcher consumers (the UI assistant) ride the same fan-out as
// Observers: every event reaches them alongside the dispatchers (they
// filter by their own sessions), and a reverse request for a session
// an Observer OWNS is answered by that observer FIRST — before the
// dispatcher fallback — so the assistant's fail-close policy never
// leaks permission asks into the room.

import (
	"context"
	"sync"

	"github.com/WWestC/Niuma_Studio/zcode"
)

// Observer is a non-dispatcher consumer of the shared bridge: it sees
// every session/state event (filtering by its own sessions) and takes
// first claim on reverse requests for sessions it owns. nil callbacks
// are skipped; an owning Observer without OnReverse falls through to
// the client's fail-close defaults (deny/decline, immediate).
type Observer struct {
	OnEvent   func(zcode.Event)
	OnState   func(sessionID, status, reason string)
	Owns      func(sessionID string) bool
	OnReverse func(method string, params map[string]any) (any, error)
}

type sharedBridge struct {
	Bridge
	mu        sync.Mutex
	sinks     []*Dispatcher
	observers []*Observer
	hooked    bool
}

func newSharedBridge(b Bridge) *sharedBridge {
	return &sharedBridge{Bridge: b}
}

// mcpOverrideBridge is Bridge's optional per-session MCP-fleet
// override (zcode.Client.CreateSessionMCP, session/create's
// mcpServers param). Declared here so the passthrough below and
// consumers asserting on the driver share one spelling.
type mcpOverrideBridge interface {
	CreateSessionMCP(ctx context.Context, workspace, mode string, servers []zcode.MCPServerSpec) (string, error)
}

// CreateSessionMCP passes a per-session MCP fleet override through to
// the live inner bridge. The decorator must re-declare the verb:
// embedding the Bridge interface promotes only Bridge's own methods,
// so without this an optional-interface assertion made on the driver
// (the assistant's warm-up probe) would miss the *zcode.Client
// underneath. An inner bridge without the verb (the test fakes)
// degrades to the plain fleet — same semantics as a caller that never
// passed an override.
func (s *sharedBridge) CreateSessionMCP(ctx context.Context, workspace, mode string, servers []zcode.MCPServerSpec) (string, error) {
	s.mu.Lock()
	inner := s.Bridge
	s.mu.Unlock()
	if mb, ok := inner.(mcpOverrideBridge); ok {
		return mb.CreateSessionMCP(ctx, workspace, mode, servers)
	}
	return inner.CreateSession(ctx, workspace, mode)
}

// swap re-points the fan-out at a replacement bridge (the previous
// app-server child died and the watchdog re-spawned it): sinks and
// observers stay registered, but the wire hooks were installed on the
// corpse — reset the marker and reinstall on the live one. Dispatchers
// keep their *sharedBridge and simply talk to the new inner client.
func (s *sharedBridge) swap(nb Bridge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Bridge = nb
	s.hooked = false
	s.hookOnce()
}

// add registers one dispatcher and installs the wire hooks once.
func (s *sharedBridge) add(d *Dispatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sinks = append(s.sinks, d)
	s.hookOnce()
}

// addObserver registers a non-dispatcher consumer and shares the same
// one-time hook install.
func (s *sharedBridge) addObserver(o *Observer) {
	if o == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, o)
	s.hookOnce()
}

// hookOnce installs the fan-out wire hooks; callers hold s.mu.
func (s *sharedBridge) hookOnce() {
	if s.hooked {
		return
	}
	s.hooked = true
	s.Bridge.SetHooks(zcode.Hooks{
		Session: s.fanEvent,
		State:   s.fanState,
	})
	s.Bridge.SetReverse(s.routeReverse)
}

// fanEvent hands the event to every sink and observer; the
// non-owning ones look the session id up, miss, and return.
func (s *sharedBridge) fanEvent(ev zcode.Event) {
	s.mu.Lock()
	sinks := append([]*Dispatcher(nil), s.sinks...)
	obs := append([]*Observer(nil), s.observers...)
	s.mu.Unlock()
	for _, d := range sinks {
		d.onEvent(ev)
	}
	for _, o := range obs {
		if o.OnEvent != nil {
			o.OnEvent(ev)
		}
	}
}

func (s *sharedBridge) fanState(sessionID, status, reason string) {
	s.mu.Lock()
	sinks := append([]*Dispatcher(nil), s.sinks...)
	obs := append([]*Observer(nil), s.observers...)
	s.mu.Unlock()
	for _, d := range sinks {
		d.onState(sessionID, status, reason)
	}
	for _, o := range obs {
		if o.OnState != nil {
			o.OnState(sessionID, status, reason)
		}
	}
}

// routeReverse answers by the owning consumer: observers first (the
// assistant's sessions are never dispatcher-owned, and its fail-close
// policy must not leak asks into the room), then the owning
// dispatcher; an unknown session (pre-birth plumbing traffic) falls to
// the first registrant — same process default on every fleet member.
func (s *sharedBridge) routeReverse(method string, params map[string]any) (any, error) {
	s.mu.Lock()
	sinks := append([]*Dispatcher(nil), s.sinks...)
	obs := append([]*Observer(nil), s.observers...)
	s.mu.Unlock()
	sid, _ := params["sessionId"].(string)
	for _, o := range obs {
		if o.Owns != nil && o.Owns(sid) {
			if o.OnReverse == nil {
				return nil, nil // fail-close defaults in the client
			}
			return o.OnReverse(method, params)
		}
	}
	for _, d := range sinks {
		if d.ownsSession(sid) {
			return d.onReverse(method, params)
		}
	}
	if len(sinks) > 0 {
		return sinks[0].onReverse(method, params)
	}
	return nil, nil // no dispatcher ever started: the client's fail-close defaults
}
