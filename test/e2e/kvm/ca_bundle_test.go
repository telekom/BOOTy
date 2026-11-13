//go:build e2e

package kvm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// privateCA is a test root CA and a server certificate for the QEMU host
// address 10.0.2.2, which the system roots of the ramdisk do not trust.
type privateCA struct {
	pem    string
	server tls.Certificate
}

func newPrivateCA(t *testing.T) privateCA {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "BOOTy e2e private root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "10.0.2.2"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("10.0.2.2")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateCA{
		pem:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		server: tls.Certificate{Certificate: [][]byte{srvDER}, PrivateKey: srvKey},
	}
}

// startPrivateCACAPRF serves the fake CAPRF (callbacks, logs, image) over
// HTTPS with a certificate of ca.
func startPrivateCACAPRF(t *testing.T, ca privateCA, imagePath string) *fakeCAPRF {
	t.Helper()
	f := &fakeCAPRF{messages: map[string]string{}}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler:           f.handler(imagePath),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{ca.server}},
	}
	go func() { _ = srv.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	f.baseURL = fmt.Sprintf("https://10.0.2.2:%d", listener.Addr().(*net.TCPAddr).Port)
	return f
}

// CA_BUNDLE_FILE: the callbacks, the connectivity check, the time sync and
// the image download go to a server with a private CA certificate, with
// TLS verification on. Then an invalid bundle stops BOOTy before the first
// request.
func TestCABundleFilePrivateCAQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)

	ca := newPrivateCA(t)
	rawDisk := createChrootCapableTestDiskImage(t, 512)
	image := compressGzip(t, rawDisk)

	t.Run("trusted", func(t *testing.T) {
		caprf := startPrivateCACAPRF(t, ca, image)
		targetDisk := filepath.Join(t.TempDir(), "ca-bundle-target.qcow2")
		run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")
		vars := caprf.nativeVars("provision")
		delete(vars, "INSECURE_TRANSPORT")
		vars["SKIP_TLS_VERIFY"] = "false"
		vars["CA_BUNDLE_FILE"] = "/deploy/ca-bundle.pem"
		vars["IMAGE"] = caprf.baseURL + "/image.gz"
		vars["DISK_DEVICE"] = "/dev/vda"
		vars["SUCCESS_ACTION"] = "poweroff"
		initramfs := buildProvisionInitramfsWithFiles(t, vars, map[string]string{"deploy/ca-bundle.pem": ca.pem})

		output := runQEMUProvision(t, findKernel(t), initramfs, targetDisk, 6*time.Minute)
		t.Logf("CA bundle output tail:\n%s", tail(output, 3000))
		assertProvisionSucceeded(t, output)
		caprf.requireStatuses(t, "init", "success")
		if !strings.Contains(string(output), "added extra root certificates") {
			t.Fatal("BOOTy did not log the CA bundle load")
		}
	})

	t.Run("invalid bundle fails closed", func(t *testing.T) {
		caprf := startPrivateCACAPRF(t, ca, image)
		targetDisk := filepath.Join(t.TempDir(), "ca-bundle-invalid.qcow2")
		run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")
		vars := caprf.nativeVars("provision")
		delete(vars, "INSECURE_TRANSPORT")
		vars["CA_BUNDLE_FILE"] = "/deploy/ca-bundle.pem"
		vars["IMAGE"] = caprf.baseURL + "/image.gz"
		vars["DISK_DEVICE"] = "/dev/vda"
		truncated := ca.pem[:len(ca.pem)/2]
		initramfs := buildProvisionInitramfsWithFiles(t, vars, map[string]string{"deploy/ca-bundle.pem": truncated})

		output := runQEMUProvision(t, findKernel(t), initramfs, targetDisk, 4*time.Minute)
		t.Logf("invalid CA bundle output tail:\n%s", tail(output, 3000))
		if !strings.Contains(string(output), "CA_BUNDLE_FILE") {
			t.Fatal("console does not name CA_BUNDLE_FILE")
		}
		caprf.requireStatuses(t)
	})
}
