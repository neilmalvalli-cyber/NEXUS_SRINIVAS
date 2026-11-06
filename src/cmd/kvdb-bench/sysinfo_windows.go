package main

import (
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

func regString(path, name string) string {
	var h syscall.Handle
	p, _ := syscall.UTF16PtrFromString(path)
	if syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, p, 0, syscall.KEY_READ, &h) != nil {
		return ""
	}
	defer syscall.RegCloseKey(h)
	nm, _ := syscall.UTF16PtrFromString(name)
	var typ, n uint32
	if syscall.RegQueryValueEx(h, nm, nil, &typ, nil, &n) != nil || n == 0 || typ != syscall.REG_SZ {
		return ""
	}
	buf := make([]uint16, n/2+1)
	if syscall.RegQueryValueEx(h, nm, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &n) != nil {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func cpuName() string {
	return strings.TrimSpace(regString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString"))
}

func osVersion() string {
	const k = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	s := regString(k, "ProductName")
	b := regString(k, "CurrentBuild")
	// Windows 11 keeps "Windows 10" in ProductName; builds >= 22000 are Windows 11.
	if n, err := strconv.Atoi(b); err == nil && n >= 22000 {
		s = strings.Replace(s, "Windows 10", "Windows 11", 1)
	}
	if b != "" {
		s += " build " + b
	}
	if d := regString(k, "DisplayVersion"); d != "" {
		s += " (" + d + ")"
	}
	return s
}
