package automation

import (
	"fmt"
	"strings"
)

// LoopTracker monitors consecutive AI actions to detect and snap out of infinite loops
type LoopTracker struct {
	consecutiveCount int
	lastActionSig    string
}

// NewLoopTracker creates a new loop prevention manager
func NewLoopTracker() *LoopTracker {
	return &LoopTracker{}
}

// TrackAction evaluates the action description and returns an optional warning/stop alert or connection break.
// Actions are considered duplicates if they target the same tool with the same arguments.
func (lt *LoopTracker) TrackAction(toolName string, args string) (consecutive int, alert string, breakConnection bool) {
	sig := fmt.Sprintf("%s:%s", strings.TrimSpace(toolName), strings.TrimSpace(args))

	if sig == lt.lastActionSig {
		lt.consecutiveCount++
	} else {
		lt.consecutiveCount = 1
		lt.lastActionSig = sig
	}

	consecutive = lt.consecutiveCount

	switch lt.consecutiveCount {
	case 3:
		alert = "⚠️ Warning: Detected consecutive duplicate action. AI might be entering an infinite loop!"
	case 4:
		alert = "🚨 Direct Stop Alert: Consecutive duplicate action threshold critical. Next repetition will break connection!"
	case 5:
		alert = "🛑 Connection Broken: Terminating active session to snap out of infinite automation loop."
		breakConnection = true
		lt.Reset()
	}

	return consecutive, alert, breakConnection
}

// Reset clears the tracked loop history
func (lt *LoopTracker) Reset() {
	lt.consecutiveCount = 0
	lt.lastActionSig = ""
}
