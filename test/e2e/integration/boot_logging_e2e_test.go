//go:build linux && e2e_boot

package integration

import (
	"strings"
	"testing"
	"time"
)

// TestBootLogLevelFromEnvironment checks LOG_LEVEL from the process
// environment (the kernel command line): the deprovision node runs with
// LOG_LEVEL=debug and logs DEBUG lines from the start on, the provision node
// keeps the default info level and logs none.
func TestBootLogLevelFromEnvironment(t *testing.T) {
	requireBootLab(t)

	for _, c := range []string{deprovisionContainer, provisionContainer} {
		if !waitForLogEntry(t, c, "starting BOOTy", 60*time.Second) {
			t.Fatalf("%s did not start BOOTy within 60s\nFull logs:\n%s", c, getBootyLogs(t, c))
		}
	}

	// loadModules runs before "starting BOOTy"; the test image has no
	// /modules directory.
	const debugLine = `level=DEBUG msg="no kernel modules directory, skipping"`
	if logs := getBootyLogs(t, deprovisionContainer); !strings.Contains(logs, debugLine) {
		t.Fatalf("deprovision node (LOG_LEVEL=debug) has no %q line\nFull logs:\n%s", debugLine, logs)
	}
	if logs := getBootyLogs(t, provisionContainer); strings.Contains(logs, "level=DEBUG") {
		t.Fatalf("provision node (default level) logged DEBUG lines\nFull logs:\n%s", logs)
	}
}
