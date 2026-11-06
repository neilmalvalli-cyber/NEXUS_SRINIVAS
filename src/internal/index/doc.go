// Package index is the in-memory hash index (DESIGN §15): key -> location of
// the key's latest PUT entry in the log, plus live-key and live-byte
// counters. Values are never held in memory.
//
// An Index is not safe for concurrent use; the database's RWMutex guards it
// (DESIGN §16). Recovery builds it single-threaded.
package index
