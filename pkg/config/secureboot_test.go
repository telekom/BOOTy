package config

import (
	"strings"
	"testing"
)

func TestValidateSecureBootPinnedDigests(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	cfg := &Config{}
	cfg.Provision.SecureBoot.PinnedDigests = map[string]string{
		"shim":   "sha256:" + validDigest,
		"grub":   validDigest,
		"kernel": validDigest,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestValidateSecureBootPinnedDigestsRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		pins    map[string]string
		wantErr string
	}{
		{
			name:    "unknown component",
			pins:    map[string]string{"initrd": strings.Repeat("a", 64)},
			wantErr: `provision.secureBoot.pinnedDigests["initrd"]: unsupported component`,
		},
		{
			name:    "bad digest",
			pins:    map[string]string{"kernel": "not-a-digest"},
			wantErr: `provision.secureBoot.pinnedDigests["kernel"]: must be 64 hex characters`,
		},
		{
			name:    "empty digest",
			pins:    map[string]string{"kernel": ""},
			wantErr: `provision.secureBoot.pinnedDigests["kernel"]: must not be empty`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Provision.SecureBoot.PinnedDigests = tc.pins

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}
