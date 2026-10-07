package server

// fleetapi.go — re-export shim: FleetAPI and FleetFuncs moved to the
// verb contract layer (server/verbs/fleetapi.go) so domain faces drink
// the fleet view without importing the shell. These aliases keep
// Options, main's adapter (boot_fleet.go) and every test unchanged;
// new code imports server/verbs directly. (Same discipline as
// chat/wire.go: this file only ever shrinks.)

import "github.com/WWestC/Niuma_Studio/server/verbs"

type FleetAPI = verbs.FleetAPI
type FleetFuncs = verbs.FleetFuncs
