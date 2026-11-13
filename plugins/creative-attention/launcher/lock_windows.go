//go:build windows

package main

import "syscall"

func pidAlive(pid int) bool {
	h, err := syscall.OpenProcess(0x1000 /* PROCESS_QUERY_LIMITED_INFORMATION */, false, uint32(pid))
	if err != nil {
		// invalid parameter means no such process; any other error (access denied): assume alive
		return err != syscall.Errno(87)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == 259 // STILL_ACTIVE
}
