package caprf

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestParseVarsDiskEncryption(t *testing.T) {
	cfg, err := ParseVars(strings.NewReader("export DISK_ENCRYPTION=\"LUKS2-TPM2\"\nexport DISK_ENCRYPTION_TARGET=\"3\"\n" +
		"export DISK_ENCRYPTION_RECOVERY_KEY_URL=\"https://p/key\"\nexport DISK_ENCRYPTION_ALLOW_FAILURE=\"true\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := config.DiskEncryptionConfig{Mode: "luks2-tpm2", Target: "3", RecoveryKeyURL: "https://p/key", AllowFailure: true}
	if cfg.Provision.DiskEncryption != want {
		t.Fatalf("got %+v, want %+v", cfg.Provision.DiskEncryption, want)
	}
	if _, err := ParseVars(strings.NewReader("export DISK_ENCRYPTION_ALLOW_FAILURE=\"ture\"\n")); err == nil {
		t.Fatal("a typo in DISK_ENCRYPTION_ALLOW_FAILURE was accepted")
	}
}

func TestParseVarsUKISigning(t *testing.T) {
	cfg, err := ParseVars(strings.NewReader("export DISK_ENCRYPTION=\"luks2-tpm2-uki\"\nexport SECUREBOOT_SIGNING=\" CI \"\n" +
		"export DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE=\"/deploy/pcr.pem\"\nexport DISK_ENCRYPTION_RECOVERY_KEY_URL=\"https://p/key\"\n" +
		"export IMAGE_COSIGN_PUBKEYS=\"/deploy/cosign.pub\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d := cfg.Provision.DiskEncryption; d.Mode != "luks2-tpm2-uki" || d.PCRPublicKeyFile != "/deploy/pcr.pem" || !d.UKI() {
		t.Fatalf("disk encryption %+v", d)
	}
	if got := cfg.Provision.SecureBoot.SigningMode(); got != "ci" {
		t.Fatalf("signing %q, want ci", got)
	}
}

func TestEscrowRecoveryKey(t *testing.T) {
	var got, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, auth = string(b), r.Header.Get("Authorization")
	}))
	defer srv.Close()
	cfg := &config.MachineConfig{}
	cfg.Transport.Token = "tok"
	cfg.Provision.DiskEncryption.RecoveryKeyURL = srv.URL + "/key"
	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var _ config.RecoveryKeyEscrow = c
	if err := c.EscrowRecoveryKey(context.Background(), []byte(`{"recoveryKey":"k"}`)); err != nil {
		t.Fatal(err)
	}
	if got != `{"recoveryKey":"k"}` || auth != "Bearer tok" {
		t.Fatalf("body %q auth %q", got, auth)
	}
}

func TestEscrowRecoveryKeyRequiresHTTPS(t *testing.T) {
	cfg := &config.MachineConfig{}
	// INSECURE_TRANSPORT does not apply to the recovery key.
	cfg.Transport.Insecure = new(true)
	cfg.Provision.DiskEncryption.RecoveryKeyURL = "http://192.0.2.1/key"
	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EscrowRecoveryKey(context.Background(), []byte("{}")); !errors.Is(err, errInsecureTransport) {
		t.Fatalf("err = %v, want errInsecureTransport", err)
	}
	cfg.Provision.DiskEncryption.RecoveryKeyURL = "https://192.0.2.1/key"
	cfg.Transport.SkipTLSVerify = new(true)
	if err := c.EscrowRecoveryKey(context.Background(), []byte("{}")); !errors.Is(err, errInsecureTransport) {
		t.Fatalf("SKIP_TLS_VERIFY: err = %v, want errInsecureTransport", err)
	}
	cfg.Provision.DiskEncryption.RecoveryKeyURL = ""
	if err := c.EscrowRecoveryKey(context.Background(), []byte("{}")); err == nil {
		t.Fatal("no URL accepted")
	}
}

// A redirect would send the key to another server: escrow follows none and
// fails on 3xx.
func TestEscrowRecoveryKeyFollowsNoRedirect(t *testing.T) {
	var other int
	otherSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other++ }))
	defer otherSrv.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherSrv.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption.RecoveryKeyURL = srv.URL + "/key"
	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EscrowRecoveryKey(context.Background(), []byte(`{"recoveryKey":"k"}`)); err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("err = %v, want a 307 failure", err)
	}
	if other != 0 {
		t.Fatalf("the redirect target got %d requests", other)
	}
}

func TestCommandPayloadCannotChangeDiskEncryption(t *testing.T) {
	base := []byte(commandBaseVars + "\nDISK_ENCRYPTION=luks2-tpm2\nDISK_ENCRYPTION_RECOVERY_KEY_URL=https://caprf/key\n")
	for _, payload := range []string{
		"DISK_ENCRYPTION_RECOVERY_KEY_URL=https://evil.example.com/key",
		"DISK_ENCRYPTION=",
		"DISK_ENCRYPTION_ALLOW_FAILURE=true",
		"DISK_ENCRYPTION_STATUS_URL=https://evil.example.com/s",
		"DISK_ENCRYPTION_SKIP_SECURE_BOOT_CHECK=true",
	} {
		if _, _, err := MergeCommandVars(base, []byte(payload)); err == nil || !strings.Contains(err.Error(), "disk encryption") {
			t.Errorf("payload %q: err = %v, want the disk encryption lock", payload, err)
		}
	}
	if _, _, err := MergeCommandVars(base, []byte("DISK_ENCRYPTION_TARGET=3")); err != nil {
		t.Errorf("target change refused: %v", err)
	}
	// Nor the signer or the pinned PCR key of the UKI mode.
	uki := []byte(commandBaseVars + "\nDISK_ENCRYPTION=luks2-tpm2-uki\nSECUREBOOT_SIGNING=ci\n" +
		"DISK_ENCRYPTION_RECOVERY_KEY_URL=https://caprf/key\nIMAGE_COSIGN_PUBKEYS=/deploy/cosign.pub\n")
	for _, payload := range []string{"SECUREBOOT_SIGNING=per-host", "DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE=/deploy/evil.pem"} {
		if _, _, err := MergeCommandVars(uki, []byte(payload)); err == nil || !strings.Contains(err.Error(), "disk encryption") {
			t.Errorf("payload %q: err = %v, want the disk encryption lock", payload, err)
		}
	}
	// A payload can not turn encryption on with its own URL either.
	if _, _, err := MergeCommandVars([]byte(commandBaseVars), []byte("DISK_ENCRYPTION=luks2-tpm2\nDISK_ENCRYPTION_RECOVERY_KEY_URL=https://evil/key")); err == nil {
		t.Error("payload turned encryption on")
	}
}

func TestDeployArchiveCannotChangeDiskEncryption(t *testing.T) {
	useDeployRoot(t)
	base := baseAgentVars + "DISK_ENCRYPTION=luks2-tpm2\nDISK_ENCRYPTION_RECOVERY_KEY_URL=https://caprf/key\n"
	c := &Client{vars: []byte(base)}
	cfg, _, err := applyArchive(c, deployArchive(t, archiveEntry{name: "deploy/vars",
		body: "HOSTNAME=w\nDISK_ENCRYPTION=\nDISK_ENCRYPTION_RECOVERY_KEY_URL=https://evil/key\nDISK_ENCRYPTION_ALLOW_FAILURE=true\nDISK_ENCRYPTION_TARGET=3\n"}))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Provision.DiskEncryption
	if d.Mode != config.DiskEncryptionLUKS2TPM2 || d.RecoveryKeyURL != "https://caprf/key" || d.AllowFailure || d.Target != "3" {
		t.Fatalf("archive changed disk encryption: %+v", d)
	}
}

// An archive without DISK_ENCRYPTION but with a partition layout would
// get the base encryption back and an invalid combination.
func TestDeployArchiveValidatesRestoredEncryption(t *testing.T) {
	useDeployRoot(t)
	base := baseAgentVars + "DISK_ENCRYPTION=luks2-tpm2\nDISK_ENCRYPTION_RECOVERY_KEY_URL=https://caprf/key\n"
	c := &Client{vars: []byte(base)}
	_, _, err := applyArchive(c, deployArchive(t, archiveEntry{name: "deploy/vars",
		body: "HOSTNAME=w\nPARTITION_LAYOUT_URL=https://caprf/layout\n"}))
	if err == nil {
		t.Fatal("archive with a partition layout accepted under disk encryption")
	}
}
