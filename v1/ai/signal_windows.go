//go:build windows

package ai

import (
	"os"
)

func syscallSignalZero() os.Signal {
	return os.Interrupt
}
