package caprf

import (
	"strings"
	"testing"
)

func TestParseVarsSecureBootApplyDBXUpdates(t *testing.T) {
	for value, want := range map[string]string{"": "<nil>", "false": "false", "0": "false", "no": "false", "1": "true", " YES ": "true"} {
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

// Off, the first boot can move PCR 7 after the binding (#976): a standby
// payload is refused, a deploy archive gets the value of the base vars.
func TestStandbyPayloadCannotChangeApplyDBXUpdates(t *testing.T) {
	for _, tc := range []struct{ base, payload string }{
		{"", "SECUREBOOT_APPLY_DBX_UPDATES=false"},
		{"SECUREBOOT_APPLY_DBX_UPDATES=false\n", "SECUREBOOT_APPLY_DBX_UPDATES=true"},
	} {
		if _, _, err := MergeCommandVars([]byte(commandBaseVars+"\n"+tc.base), []byte(tc.payload)); err == nil || !strings.Contains(err.Error(), "SECUREBOOT_APPLY_DBX_UPDATES") {
			t.Errorf("base %q payload %q: err = %v, want the lock", tc.base, tc.payload, err)
		}
	}
	if _, _, err := MergeCommandVars([]byte(commandBaseVars+"\nSECUREBOOT_APPLY_DBX_UPDATES=false\n"), []byte("SECUREBOOT_APPLY_DBX_UPDATES=0")); err != nil {
		t.Errorf("unchanged value refused: %v", err)
	}
	useDeployRoot(t)
	c := &Client{vars: []byte(baseAgentVars)}
	cfg, _, err := applyArchive(c, deployArchive(t, archiveEntry{name: "deploy/vars", body: "HOSTNAME=w\nSECUREBOOT_APPLY_DBX_UPDATES=false\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provision.SecureBoot.ApplyDBXUpdates != nil {
		t.Fatalf("the deploy archive turned the Secure Boot updates off: %v", *cfg.Provision.SecureBoot.ApplyDBXUpdates)
	}
}
