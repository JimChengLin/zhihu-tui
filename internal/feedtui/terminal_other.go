//go:build !darwin && !linux

package feedtui

import (
	"fmt"
	"os"
)

type terminalState struct{}

func isTerminal(*os.File) bool {
	return false
}

func makeRaw(*os.File) (*terminalState, error) {
	return nil, fmt.Errorf("feed TUI is not supported on this operating system")
}

func restoreTerminal(*os.File, *terminalState) {}

func terminalSize(*os.File) (int, int, error) {
	return 0, 0, fmt.Errorf("feed TUI is not supported on this operating system")
}
