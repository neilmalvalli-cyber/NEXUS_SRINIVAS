//go:build !windows

package platform

import "os"

// SyncDir fsyncs a directory so that a create or rename inside it survives a
// power loss (DESIGN §6.4: "Unix: fsync directory").
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	serr := d.Sync()
	cerr := d.Close()
	if serr != nil {
		return serr
	}
	return cerr
}
