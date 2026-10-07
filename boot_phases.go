package main

// boot_phases.go — the composition root's PHASES. main() is a short,
// ordered list of calls into the boot_phase_* family; each phase's
// signature IS its dependency contract (what it consumes and produces
// — the hand-off structs live in boot_sets.go), and each one's doc
// comment carries the ordering invariants that used to live only
// inside a 1,200-line main(). The rule for edits: a phase may be
// reordered only if the data flow below still type-checks AND its doc
// comment's invariants still read true — the compiler covers the
// first, the comment covers the second, nothing is implicit anymore.
//
// Body discipline: each phase opens by aliasing its inputs to the
// historical local names, so the bodies are byte-for-byte the code
// that lived in main() (pure extraction, zero logic edits).
//
// The family: boot_phase_stores.go (openStores) → boot_phase_dispatch.go
// (bootDispatch) → boot_phase_face.go (startServerFace) →
// boot_phase_shell.go (runShell). The split of this file (v3 结构手术)
// kept every body byte-identical — one file per phase, so the next
// boot step lands in the file it belongs to instead of growing a
// shared one.

// bootStoresParallel gates openStores' parallel leg fan-out. It is a
// var (not a const) on purpose: the cold-boot timing test flips it to
// false to measure the serial baseline in the same binary — the A/B
// pair is recorded in the flipping commit's acceptance record.
var bootStoresParallel = true
