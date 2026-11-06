// Package storage is the open database handle: the single commit path
// (DESIGN §6: encode the frame, write it strictly sequentially at the end of
// the log, Sync, then update the index), rollback and the FAILED state
// (§6.3, §13.2), the read path (§7, §15.2), and Close.
//
// Concurrency (docs/PERFORMANCE_OPTIMIZATION.md): wmu serializes every
// change (commits, compaction, Close); only its holder writes, which keeps
// invariant I1 (§2.4): at most one unsynced frame, always the last one. mu
// (RWMutex) protects reader-visible state and is held exclusively only to
// publish after Sync. Lock order: wmu before mu. Concurrent commits queued
// during a Sync are merged into one frame and one Sync (group commit, no
// timer); each caller returns only after the Sync covering it. GETs use a
// small pool of read-only handles (readpool.go).
package storage
