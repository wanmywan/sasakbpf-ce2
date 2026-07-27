//go:build linux

package main

import (
	"syscall"
	"unsafe"
)

// PR_SET_NAME = 15 on Linux. Sets /proc/self/comm (16-byte limited).
func setProcName(name string) {
	if len(name) > 15 {
		name = name[:15]
	}
	b := []byte(name + "\x00")
	_, _, errno := syscall.Syscall(syscall.SYS_PRCTL, 15, uintptr(unsafe.Pointer(&b[0])), 0)
	_ = errno
}