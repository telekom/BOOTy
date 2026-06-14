//go:build linux

package image

import (
	"strings"
	"testing"
)

func TestRedactQCOW2SourceForLog(t *testing.T) {
	got := redactQCOW2SourceForLog("https://user:secret@example.com/images/node.qcow2?token=abc#frag")
	for _, leaked := range []string{"user", "secret", "token=abc", "frag"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted qcow2 source leaked %q: %s", leaked, got)
		}
	}
	if got != "https://example.com/images/node.qcow2" {
		t.Fatalf("redactQCOW2SourceForLog() = %q", got)
	}
}
