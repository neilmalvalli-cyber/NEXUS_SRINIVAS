//go:build !windows

package proc

// PeakMemory is only implemented on Windows; elsewhere it reports 0 (unknown).
func (s *Session) PeakMemory() uint64 { return 0 }
