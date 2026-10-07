// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package image

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestSourceDiagnosticCharacterization(t *testing.T) {
	source := &url.URL{
		Scheme: "https", Host: "example.test", Path: "/node.img",
		User:     url.UserPassword("fixture-user", "fixture-password"),
		RawQuery: "token=fixture-token", Fragment: "fixture-fragment",
	}
	withoutFragment := *source
	withoutFragment.Fragment = ""
	for _, diagnostic := range []string{source.String(), source.Redacted(), withoutFragment.Redacted()} {
		t.Run(diagnostic, func(t *testing.T) {
			cause := errors.New("download failed: " + diagnostic)
			wrapped := &redactedSourceError{rawSource: source.String(), err: cause}
			for _, secret := range []string{"fixture-user", "fixture-password", "fixture-token", "fixture-fragment"} {
				if strings.Contains(wrapped.Error(), secret) {
					t.Fatalf("diagnostic leaked %q: %s", secret, wrapped)
				}
			}
			if !strings.Contains(wrapped.Error(), "https://example.test/node.img") {
				t.Fatalf("diagnostic lost source context: %s", wrapped)
			}
			if !errors.Is(wrapped, cause) {
				t.Fatal("diagnostic lost its cause")
			}
		})
	}
	if got := RedactSourceError(nil, source.String()); got != "" {
		t.Fatalf("nil error = %q", got)
	}
}
