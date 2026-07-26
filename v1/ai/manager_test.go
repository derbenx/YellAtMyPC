package ai

import (
	"testing"
)

func TestFindLocalFiles(t *testing.T) {
	// Should run without crashing even if the folders are missing
	servers, ggufs, mmprojs := FindLocalFiles()

	// Print scanned directories count
	t.Logf("Found %d local servers, %d GGUFs, and %d mmprojs in default layout", len(servers), len(ggufs), len(mmprojs))
}
