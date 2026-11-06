// Package storage is the open database handle: the single commit path
// (DESIGN §6: encode the frame, write it strictly sequentially at the end of
// the log, Sync, then update the index), rollback and the FAILED state
// (§6.3, §13.2), the read path (§7, §15.2), and Close.
//
// Concurrency: one sync.RWMutex per handle (§16). Writers hold it
// exclusively through write, Sync and index update, which is what keeps
// invariant I1 (§2.4): at most one unsynced frame, always the last one.
//
// Not implemented yet (Phase 3): max_bytes admission and compaction.
package storage
