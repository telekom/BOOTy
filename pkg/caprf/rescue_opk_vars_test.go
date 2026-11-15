package caprf

import (
	"strings"
	"testing"
)

const (
	testOPKProviders = `"https://idp.example booty 24h"`
	testOPKPolicy    = `"root alice@example.com https://idp.example"`
)

func TestParseVarsRescueOPK(t *testing.T) {
	cfg, err := ParseVars(strings.NewReader("RESCUE_SSH_OPK_PROVIDERS=" + testOPKProviders + "\nRESCUE_SSH_OPK_POLICY=" + testOPKPolicy + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rescue.SSHOPKProviders != "https://idp.example booty 24h" || cfg.Rescue.SSHOPKPolicy != "root alice@example.com https://idp.example" {
		t.Fatalf("rescue = %+v", cfg.Rescue)
	}
}

// A standby payload or deploy archive must not add an identity provider or
// an identity to the rescue SSH login.
func TestStandbyPayloadCannotChangeRescueOPK(t *testing.T) {
	base := commandBaseVars + "\nRESCUE_SSH_OPK_PROVIDERS=" + testOPKProviders + "\n"
	for _, payload := range []string{
		"RESCUE_SSH_OPK_PROVIDERS=\"https://evil.example x 1week\"",
		"RESCUE_SSH_OPK_PROVIDERS=",
		"RESCUE_SSH_OPK_POLICY=\"root mallory@evil.example https://idp.example\"",
	} {
		if _, _, err := MergeCommandVars([]byte(base), []byte(payload)); err == nil || !strings.Contains(err.Error(), "rescue SSH access") {
			t.Errorf("payload %q: err = %v, want the lock", payload, err)
		}
	}
	useDeployRoot(t)
	c := &Client{vars: []byte(baseAgentVars + "\nRESCUE_SSH_OPK_POLICY=" + testOPKPolicy + "\n")}
	cfg, _, err := applyArchive(c, deployArchive(t, archiveEntry{name: "deploy/vars",
		body: "HOSTNAME=w\nRESCUE_SSH_OPK_PROVIDERS=\"https://evil.example x 1week\"\nRESCUE_SSH_OPK_POLICY=\"root mallory@evil.example https://evil.example\"\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rescue.SSHOPKProviders != "" || cfg.Rescue.SSHOPKPolicy != "root alice@example.com https://idp.example" {
		t.Fatalf("the deploy archive changed the opkssh settings: %+v", cfg.Rescue)
	}
}
