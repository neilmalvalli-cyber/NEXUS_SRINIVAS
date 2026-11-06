//go:build !windows

package platform

import "os"

// MoveNoReplace renames from to to, failing if to already exists.
//
// Go's standard library does not expose renameat2(RENAME_NOREPLACE), so this
// uses link(2), which fails with EEXIST if the destination exists, followed by
// unlink of the source. A kill between the two steps leaves both names
// pointing at the same complete file; the recovery authority table handles
// that, because a valid `path` always wins and stale temp names are deleted
// (docs/DESIGN.md §11.4). Requires a filesystem with hard links.
//
// Linux is a secondary target; this file is compiled and vetted for
// linux/amd64 but its tests have not been run on Linux yet.
func MoveNoReplace(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	return os.Remove(from)
}
