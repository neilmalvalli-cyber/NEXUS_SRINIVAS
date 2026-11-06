//go:build windows

package platform

// SyncDir is a no-op on Windows. NTFS directory-entry changes (create,
// MoveFileW) survive process termination without an explicit flush, which is
// the official crash model; power-loss durability is not claimed on Windows
// (DESIGN §2.3, §6.4). Go cannot open a directory handle for
// FlushFileBuffers through the os package.
func SyncDir(dir string) error { return nil }
