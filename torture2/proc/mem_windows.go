//go:build windows

package proc

import (
	"syscall"
	"unsafe"
)

var procGetProcessMemoryInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// PeakMemory returns the peak working set of the running process in bytes,
// or 0 if it cannot be read (the process already exited).
func (s *Session) PeakMemory() uint64 {
	const processQueryLimitedInformation = 0x1000
	const processVMRead = 0x0010
	h, err := syscall.OpenProcess(processQueryLimitedInformation|processVMRead, false, uint32(s.Pid()))
	if err != nil {
		return 0
	}
	defer syscall.CloseHandle(h)
	if procGetProcessMemoryInfo.Find() != nil {
		return 0
	}
	var c processMemoryCounters
	c.cb = uint32(unsafe.Sizeof(c))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	if r == 0 {
		return 0
	}
	return uint64(c.PeakWorkingSetSize)
}
