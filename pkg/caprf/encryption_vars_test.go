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
	cfg.Provision.DiskEncryption.RecoveryKeyURL = "http://192.0.2.1/key"
	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EscrowRecoveryKey(context.Background(), []byte("{}")); !errors.Is(err, errInsecureTransport) {
		t.Fatalf("err = %v, want errInsecureTransport", err)
	}
	cfg.Provision.DiskEncryption.RecoveryKeyURL = ""
	if err := c.EscrowRecoveryKey(context.Background(), []byte("{}")); err == nil {
		t.Fatal("no URL accepted")
	}
}
