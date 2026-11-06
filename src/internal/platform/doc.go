// Package platform isolates every operating-system-specific detail the engine
// depends on, so the rest of the code never mentions a Windows error number
// or a Unix errno.
//
// Contents (one implementation per OS, selected by file name / build tag):
//
//   - IsDiskFull, IsSharingViolation: classify OS errors. The raw Windows
//     codes live only in errors_windows.go.
//   - MoveNoReplace: rename that FAILS if the destination exists. It is the
//     primitive for the compaction renames R1/R2 and for adopting .new /
//     .compact / .old during recovery (docs/DESIGN.md §11). os.Rename must
//     never be used for those: on Windows it calls MoveFileEx with
//     MOVEFILE_REPLACE_EXISTING and silently overwrites the destination.
//
// Phase 1 status: these primitives and their tests exist; nothing in the
// engine calls them yet. The process lock file is specified in
// docs/DESIGN.md §16.1 and is implemented in Phase 2 (package locking).
package platform
