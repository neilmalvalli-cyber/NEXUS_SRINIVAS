// Package recovery opens a data file after any crash: it resolves file
// authority among path/.compact/.old/.new, validates the superblock, scans
// frames, classifies a damaged tail and truncates only proven tail damage.
//
//   - authority.go: the authority table (DESIGN §11.4, rules A1–A7), safe
//     creation through .new, Prepare (lock, then resolve), RemoveStale.
//   - scan.go: Scan, the read-only frame scanner with tail rules C0–C8
//     (DESIGN §8.2) that rebuilds the index.
//   - open.go: Open, the whole recovery sequence of DESIGN §8, returning a
//     State (lock, read-write file, index, next sequence number) for the
//     write path.
package recovery
