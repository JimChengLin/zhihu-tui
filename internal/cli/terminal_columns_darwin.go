//go:build darwin

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

type terminalWindowSize struct {
	row    uint16
	column uint16
	xpixel uint16
	ypixel uint16
}

func terminalColumns() (int, bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return 0, false
	}
	defer tty.Close()
	var size terminalWindowSize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
	if errno != 0 || size.column == 0 {
		return 0, false
	}
	return int(size.column), true
}
