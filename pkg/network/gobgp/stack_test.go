//go:build linux

package gobgp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestStackLogPollingTargetRedactsSensitiveURLParts(t *testing.T) {
	var logs bytes.Buffer
	stack := &Stack{
		log: slog.New(slog.NewTextHandler(&logs, nil)),
	}

	stack.logPollingTarget("https://user:super-secret@example.telekom.de/images/provisioner.iso?token=abc123#frag")

	got := logs.String()
	for _, leaked := range []string{"user", "super-secret", "token=abc123", "frag"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("logs leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, `target=https://example.telekom.de/images/provisioner.iso`) {
		t.Fatalf("logs = %q, want redacted target URL", got)
	}
}
