// Package compaction is intentionally empty.
//
// Compaction needs the open handle's file, index and lock, so it is
// implemented next to the commit path in package storage (compact.go):
// snapshot planning, the sealed .compact file and the two non-overwriting
// moves of DESIGN §11. The pure size and trigger rules live in package
// admission.
package compaction
