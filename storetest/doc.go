// Package storetest holds the portable contract suites for the
// ledger family: one suite per domain (requirements, plan, merge,
// meeting, notice, tasks) pinning the ENGINE's semantics — per-project
// id counters with a restart-proof high-water, the state machines
// (open/split/parking/closed; submit/supersede/take; expect_rev), the
// renumber/remap companion faces, drop semantics — plus the
// durability round-trips (reopen must read what was written; a
// deleted id is never re-issued).
//
// Since the sqlite promotion there is ONE implementation per domain
// (the engine) over two backings: the in-memory nil seam and the
// single-database document seams. Both run the SAME suites — that is
// the promotion's gate (the JSON era's second full implementation,
// and its byte-parity test, are gone with it).
//
// A backing wires in through OpenBacking: it yields an opener (one
// engine bound to one storage subject) plus a reopen function bound
// to the same durable backing (the crash-and-restart simulation — it
// must construct a new instance reading the same bytes, never hand
// back the warm in-memory one).
package storetest
