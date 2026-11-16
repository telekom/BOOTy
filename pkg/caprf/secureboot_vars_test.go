package caprf

import (
	"strings"
	"testing"
)

func TestParseVarsSecureBootApplyDBXUpdates(t *testing.T) {
	for value, want := range map[string]string{"false": "false", "0": "false", "1": "true"} {
		cfg, err := ParseVars(strings.NewReader("SECUREBOOT_APPLY_DBX_UPDATES=" + value + "\n"))
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		got := "<nil>"
		if c := cfg.Provision.SecureBoot.ApplyDBXUpdates; c != nil {
			got = map[bool]string{true: "true", false: "false"}[*c]
		}
		if got != want {
			t.Errorf("SECUREBOOT_APPLY_DBX_UPDATES=%q: %s, want %s", value, got, want)
		}
	}
	if _, err := ParseVars(strings.NewReader("SECUREBOOT_APPLY_DBX_UPDATES=flase\n")); err == nil {
		t.Fatal("a typo in SECUREBOOT_APPLY_DBX_UPDATES was accepted")
	}
}
