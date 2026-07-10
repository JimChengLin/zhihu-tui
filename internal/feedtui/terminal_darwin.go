//go:build darwin

package feedtui

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

type terminalState syscall.Termios

type terminalWindowSize struct {
	row    uint16
	column uint16
	xpixel uint16
	ypixel uint16
}

func isTerminal(file *os.File) bool {
	var state terminalState
	return terminalIoctl(file.Fd(), syscall.TIOCGETA, unsafe.Pointer(&state)) == nil
}

func makeRaw(file *os.File) (*terminalState, error) {
	var state terminalState
	if err := terminalIoctl(file.Fd(), syscall.TIOCGETA, unsafe.Pointer(&state)); err != nil {
		return nil, err
	}
	raw := state
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := terminalIoctl(file.Fd(), syscall.TIOCSETA, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return &state, nil
}

func restoreTerminal(file *os.File, state *terminalState) {
	if state != nil {
		_ = terminalIoctl(file.Fd(), syscall.TIOCSETA, unsafe.Pointer(state))
	}
}

func terminalSize(file *os.File) (int, int, error) {
	var size terminalWindowSize
	if err := terminalIoctl(file.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&size)); err != nil {
		return 0, 0, err
	}
	if size.column == 0 || size.row == 0 {
		return 0, 0, fmt.Errorf("terminal returned an empty window size")
	}
	return int(size.column), int(size.row), nil
}

func terminalIoctl(fd uintptr, request uintptr, data unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(data))
	if errno != 0 {
		return errno
	}
	return nil
}
