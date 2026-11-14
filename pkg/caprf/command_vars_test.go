package caprf

import (
	"slices"
	"strings"
	"testing"
)

const commandBaseVars = `export MODE="standby"
export HOSTNAME="t1"
export TOKEN="tok"
export MACHINE_EXTRA_KERNEL_PARAMS="intel_iommu=on"
export EXTRA_KERNEL_PARAMS="fromvars=t1"
export HEARTBEAT_URL="https://caprf.example.com/agent/heartbeat"
export COMMANDS_URL="https://caprf.example.com/agent/commands"
export STATIC_IP="10.0.0.2/24"
`

func TestMergeCommandVarsPayloadWins(t *testing.T) {
	cfg, keys, err := MergeCommandVars([]byte(commandBaseVars),
		[]byte("export HOSTNAME=\"t1-payload\"\nEXTRA_KERNEL_PARAMS=frompayload=t1\n# comment\nHOSTNAME=t1-payload\n"))
	if err != nil {
		t.Fatalf("MergeCommandVars: %v", err)
	}
	if cfg.Hostname != "t1-payload" {
		t.Fatalf("hostname = %q, want the payload value", cfg.Hostname)
	}
	// EXTRA_KERNEL_PARAMS of the payload replaces the base entry and still
	// goes after MACHINE_EXTRA_KERNEL_PARAMS, like in a vars file.
	if cfg.Provision.ExtraKernelParams != "intel_iommu=on frompayload=t1" {
		t.Fatalf("extra kernel params = %q", cfg.Provision.ExtraKernelParams)
	}
	if !slices.Equal(keys, []string{"EXTRA_KERNEL_PARAMS", "HOSTNAME"}) {
		t.Fatalf("keys = %v", keys)
	}
	if cfg.Transport.Token != "tok" || cfg.Mode != "standby" {
		t.Fatalf("base values lost: token %q mode %q", cfg.Transport.Token, cfg.Mode)
	}
}

// An empty payload value clears the base value, like `source vars`.
func TestMergeCommandVarsEmptyIntegerClearsBase(t *testing.T) {
	cfg, keys, err := MergeCommandVars([]byte(commandBaseVars+"export NUM_VFS=\"8\"\n"), []byte("NUM_VFS=\n"))
	if err != nil {
		t.Fatalf("MergeCommandVars: %v", err)
	}
	if cfg.Provision.Disk.NumVFs != 0 {
		t.Fatalf("NumVFs = %d, want 0 after an empty payload value", cfg.Provision.Disk.NumVFs)
	}
	if !slices.Equal(keys, []string{"NUM_VFS"}) {
		t.Fatalf("keys = %v", keys)
	}
}

func TestMergeCommandVarsRejectsRuntimeSettings(t *testing.T) {
	for payload, want := range map[string]string{
		"TOKEN=other":                                "transport",
		"INIT_URL=https://x.example.com/":            "transport",
		"SKIP_TLS_VERIFY=true":                       "transport",
		"COMMANDS_URL=https://x/agent/c":             "agent",
		"STATIC_IP=10.0.0.9/24":                      "network",
		"MODE=provision":                             "MODE",
		"DRY_RUN=true":                               "DRY_RUN",
		"CAPRF_LEGACY_COMPAT=true":                   "CAPRF_LEGACY_COMPAT",
		"STANDBY_DEPROVISION_SUCCESS_ACTION=standby": "agent",
		"no assignment":                              "line 1 is not KEY=VALUE",
		"HOSTNAME=x\nno assignment":                  "line 2 is not KEY=VALUE",
		"HOSTNMAE=typo\nHOSTNAME=x":                  "unknown payload keys HOSTNMAE",
		"HOSTNAME =x":                                "unknown payload keys HOSTNAME ",
		"INVENTORY_URL=https://x.example.com/inv":    "INVENTORY_URL",
		"FIRMWARE_URL=https://x.example.com/fw":      "FIRMWARE_URL",
		"HEALTH_CHECK_URL=https://x.example.com/h":   "HEALTH_CHECK_URL",
		"METRICS_URL=https://x.example.com/m":        "telemetry",
		"CRASH_ARTIFACTS_UPLOAD_URL=https://x/u":     "crash artifact",
		"LOG_URL=https://x.example.com/log":          "transport",
		"SUCCESS_ACTION=later":                       "payload keys SUCCESS_ACTION do not parse or validate",
		"LUKS_DEVICE=/dev/sda":                       "LUKS_DEVICE",
		"SYSEXT_COSIGN_PUBKEYS=/deploy/other.pub":    "cosign",
		"IMAGE_COSIGN_IDENTITY_REGEXP=.*":            "cosign",
		"IMAGE_COSIGN_OIDC_ISSUER=https://x.example": "cosign",
		"SYSEXT_COSIGN_TRUSTED_ROOT=/deploy/tr.json": "cosign",
		"SYSEXT_COSIGN_IDENTITY_REGEXP=.*":           "cosign",
		"SYSEXT_COSIGN_OIDC_ISSUER=https://x":        "cosign",
		"IMAGE_FOLLOW_REDIRECTS=false":               "IMAGE_FOLLOW_REDIRECTS",
	} {
		_, _, err := MergeCommandVars([]byte(commandBaseVars), []byte(payload))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("payload %q: error = %v, want it to name %q", payload, err, want)
		}
	}
}

// A payload can not clear or change a cosign policy of the base vars; it
// may repeat it.
func TestMergeCommandVarsKeepsCosignPolicy(t *testing.T) {
	base := commandBaseVars + `IMAGE_COSIGN_PUBKEYS=/deploy/c.pub
IMAGE_COSIGN_TRUSTED_ROOT=/deploy/trusted_root.json
IMAGE_COSIGN_IDENTITY_REGEXP=https://ci\.example/.*
IMAGE_COSIGN_OIDC_ISSUER=https://ci.example
SYSEXT_COSIGN_TRUSTED_ROOT=/deploy/trusted_root.json
`
	for _, payload := range []string{
		"IMAGE_COSIGN_PUBKEYS=",
		"IMAGE_COSIGN_TRUSTED_ROOT=",
		"IMAGE_COSIGN_IDENTITY_REGEXP=.*",
		"IMAGE_COSIGN_OIDC_ISSUER=https://other.example",
		"SYSEXT_COSIGN_TRUSTED_ROOT=",
	} {
		if _, _, err := MergeCommandVars([]byte(base), []byte(payload)); err == nil || !strings.Contains(err.Error(), "cosign") {
			t.Errorf("payload %q: error = %v, want a cosign rejection", payload, err)
		}
	}
	if _, _, err := MergeCommandVars([]byte(base), []byte("IMAGE_COSIGN_OIDC_ISSUER=https://ci.example\nHOSTNAME=x")); err != nil {
		t.Errorf("repeating the policy: %v", err)
	}
}

// The payload sees the presets of the merged file: SUCCESS_ACTION in the
// payload wins over the CAPRF_LEGACY_COMPAT preset of the base.
func TestMergeCommandVarsKeepsPresetRules(t *testing.T) {
	base := commandBaseVars + "CAPRF_LEGACY_COMPAT=true\n"
	cfg, _, err := MergeCommandVars([]byte(base), []byte("SUCCESS_ACTION=reboot"))
	if err != nil {
		t.Fatalf("MergeCommandVars: %v", err)
	}
	if cfg.SuccessAction != "reboot" || cfg.FailureAction != "wait" {
		t.Fatalf("success %q failure %q, want the payload value and the preset", cfg.SuccessAction, cfg.FailureAction)
	}
}

// Blank and comment lines are no payload: no keys, no config.
func TestMergeCommandVarsEmptyPayload(t *testing.T) {
	for _, payload := range []string{"", "  \n\t\n", "# only a comment\n\n"} {
		cfg, keys, err := MergeCommandVars([]byte(commandBaseVars), []byte(payload))
		if err != nil || cfg != nil || len(keys) != 0 {
			t.Errorf("payload %q: cfg %v keys %v err %v, want no payload", payload, cfg, keys, err)
		}
	}
}

// A rejection names keys, never values: the error goes into the ACK.
func TestMergeCommandVarsErrorsHaveNoValues(t *testing.T) {
	for _, payload := range []string{"SUCCESS_ACTION=s3cr3t", "MIN_DISK_SIZE_GB=s3cr3t", "SYSEXT_LAYERS=s3cr3t"} {
		_, _, err := MergeCommandVars([]byte(commandBaseVars), []byte(payload))
		if err == nil || strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("payload %q: error %v, want an error without the value", payload, err)
		}
	}
}

// Only the key that fails is named.
func TestMergeCommandVarsNamesOnlyTheBadKey(t *testing.T) {
	_, _, err := MergeCommandVars([]byte(commandBaseVars), []byte("HOSTNAME=ok\nSUCCESS_ACTION=s3cr3t\n"))
	if err == nil || err.Error() != "payload keys SUCCESS_ACTION do not parse or validate" {
		t.Fatalf("error = %v", err)
	}
}

// Under a cosign policy a payload can not swap the signed oci:// image for
// an HTTP one that only a payload checksum checks (#933 review), nor change
// the checksum of a base HTTP source; it may switch to another oci://
// image, which is verified.
func TestMergeCommandVarsKeepsCosignCoverage(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	signed := commandBaseVars + "IMAGE=oci://registry.example/os" + digest + "\nIMAGE_COSIGN_PUBKEYS=/deploy/c.pub\n" +
		"SYSEXT_COSIGN_TRUSTED_ROOT=/deploy/tr.json\nSYSEXT_COSIGN_IDENTITY_REGEXP=.*\nSYSEXT_COSIGN_OIDC_ISSUER=https://ci.example\n"
	checksum := "IMAGE_CHECKSUM=" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, base, payload string
		ok                  bool
	}{
		{name: "http image with own checksum", base: signed, payload: "IMAGE=https://evil.example/os.img\n" + checksum},
		{name: "http fallback url", base: signed, payload: "IMAGE=oci://registry.example/os" + digest + ",https://evil.example/os.img\n" + checksum},
		{name: "http sysext", base: signed, payload: `SYSEXT_LAYERS=[{"name":"x","source":"https://evil.example/x.raw","sha256":"` + strings.Repeat("c", 64) + `"}]`},
		{name: "other oci image", base: signed, payload: "IMAGE=oci://registry.example/os@sha256:" + strings.Repeat("d", 64), ok: true},
		{name: "checksum of a base http source", base: signed + "IMAGE=https://ok.example/os.img\n" + checksum + "\n",
			payload: "IMAGE_CHECKSUM=" + strings.Repeat("e", 64)},
		{name: "drop the base http source", base: signed + "IMAGE=https://ok.example/os.img\n" + checksum + "\n",
			payload: "IMAGE=oci://registry.example/os" + digest, ok: true},
		{name: "no policy", base: commandBaseVars, payload: "IMAGE=https://ok.example/os.img\n" + checksum, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := MergeCommandVars([]byte(tc.base), []byte(tc.payload))
			if tc.ok {
				if err != nil {
					t.Fatalf("MergeCommandVars() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "not oci://") {
				t.Fatalf("MergeCommandVars() = %v, want a refusal of the uncovered source", err)
			}
		})
	}
}
