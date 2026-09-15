package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a real terminal, using the TCGETS
// ioctl every terminal driver answers and every non-terminal file,
// including /dev/null, refuses. A character device is not enough by
// itself: /dev/null is a character device but never a terminal, so a
// mode-bit check alone misreads it as one. This is the one seam both
// gc's --force-after confirmation and main's progress-on-terminal check
// go through.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
