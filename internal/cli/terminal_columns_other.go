//go:build !darwin

package cli

func terminalColumns() (int, bool) {
	return 0, false
}
