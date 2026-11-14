package caprf

import (
	"strings"
	"testing"
)

func TestParseVarsDeprovisionCryptoErase(t *testing.T) {
	for value, want := range map[string]string{"": "<nil>", "false": "false", "0": "false", "true": "true", "YES": "true"} {
		cfg, err := ParseVars(strings.NewReader("DEPROVISION_CRYPTO_ERASE=" + value + "\n"))
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		got := "<nil>"
		if c := cfg.Deprovision.CryptoErase; c != nil {
			got = map[bool]string{true: "true", false: "false"}[*c]
		}
		if got != want {
			t.Errorf("DEPROVISION_CRYPTO_ERASE=%q: %s, want %s", value, got, want)
		}
	}
	// A typo must not turn the crypto-erase off.
	if _, err := ParseVars(strings.NewReader("DEPROVISION_CRYPTO_ERASE=flase\n")); err == nil {
		t.Fatal("a typo in DEPROVISION_CRYPTO_ERASE was accepted")
	}
}

func TestStandbyPayloadCannotChangeCryptoErase(t *testing.T) {
	for _, tc := range []struct{ base, payload string }{
		{"", "DEPROVISION_CRYPTO_ERASE=false"},
		{"DEPROVISION_CRYPTO_ERASE=false\n", "DEPROVISION_CRYPTO_ERASE=true"},
	} {
		if _, _, err := MergeCommandVars([]byte(commandBaseVars+"\n"+tc.base), []byte(tc.payload)); err == nil || !strings.Contains(err.Error(), "DEPROVISION_CRYPTO_ERASE") {
			t.Errorf("base %q payload %q: err = %v, want the lock", tc.base, tc.payload, err)
		}
	}
	useDeployRoot(t)
	c := &Client{vars: []byte(baseAgentVars)}
	cfg, _, err := applyArchive(c, deployArchive(t, archiveEntry{name: "deploy/vars", body: "HOSTNAME=w\nDEPROVISION_CRYPTO_ERASE=false\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Deprovision.CryptoErase != nil {
		t.Fatalf("the deploy archive turned the crypto-erase off: %v", *cfg.Deprovision.CryptoErase)
	}
}
