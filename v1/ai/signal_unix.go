//go:build !windows

package ai

import (
	"os"
	"syscall"
)

func syscallSignalZero() os.Signal {
	return syscall.Signal(0)
}
