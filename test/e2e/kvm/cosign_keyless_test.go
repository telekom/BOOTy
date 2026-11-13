//go:build e2e

package kvm

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"

	imageutil "github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/image/sigstoretest"
)

// Scenario cosign-keyless (.github/workflows/kvm-matrix.yml) runs
// ^TestKeylessCosign: the layered-layout fixture as an oci:// image with
// IMAGE_COSIGN_TRUSTED_ROOT, signed keyless by a local Sigstore
// (pkg/image/sigstoretest: CA, CT log and Rekor keys made at test time, so
// there are no recorded fixtures to refresh).

const (
	keylessTrustedRootPath = "deploy/trusted_root.json"
	keylessIdentity        = "https://ci.example/os/image//.ci/build.yml@refs/heads/main"
	keylessIssuer          = "https://ci.example"
)

// TestKeylessCosignSignedOCIImageVM provisions the fixture from an image
// with a cosign 3 bundle referrer and preloads a sysext with a cosign 2
// .sig tag signature; both are verified against the trusted root and the
// identity before the wipe.
func TestKeylessCosignSignedOCIImageVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	ss := newKeylessTestSigstore(t)
	pushHost, guestHost := startABOCIRegistry(t)
	imageDigest := pushCosignTestImage(t, pushHost, "test/layered-os",
		static.NewLayer(readFile(t, compressGzip(t, createLayeredLayoutTestDiskImage(t, 512))), types.OCILayer))
	signKeylessBundle(t, ss, pushHost+"/test/layered-os@"+imageDigest, keylessIdentity)

	sysextPayload := []byte("keyless signed sysext vm e2e\n")
	sysextDigest := pushCosignTestImage(t, pushHost, "test/node-tuning",
		stream.NewLayer(io.NopCloser(bytes.NewReader(sysextPayload)), stream.WithMediaType(imageutil.SystemdSysextMediaType)))
	sysextRef, err := name.NewDigest(pushHost + "/test/node-tuning@" + sysextDigest)
	if err != nil {
		t.Fatal(err)
	}
	payload := sigstoretest.SimpleSigningPayload(sysextRef.Context().Name(), sysextDigest)
	annotations, err := ss.Legacy(payload, sigstoretest.Options{Identity: keylessIdentity, Issuer: keylessIssuer})
	if err != nil {
		t.Fatal(err)
	}
	if err := sigstoretest.PushLegacy(sysextRef, payload, annotations); err != nil {
		t.Fatal(err)
	}

	sysextLayers := `[{"name":"node-tuning","version":"vm-e2e","source":"oci://` + guestHost + `/test/node-tuning@` + sysextDigest +
		`","fileName":"node-tuning.raw","mode":"preload"}]`
	output, targetDisk := runKeylessProvision(t, ss, "keyless-signed", "oci://"+guestHost+"/test/layered-os@"+imageDigest, map[string]string{
		"SYSEXT_ENABLED":                "true",
		"SYSEXT_DEFAULT_MODE":           "preload",
		"SYSEXT_LAYERS":                 sysextLayers,
		"SYSEXT_COSIGN_TRUSTED_ROOT":    "/" + keylessTrustedRootPath,
		"SYSEXT_COSIGN_IDENTITY_REGEXP": `https://ci\.example/os/image//\.ci/build\.yml@refs/heads/main`,
		"SYSEXT_COSIGN_OIDC_ISSUER":     keylessIssuer,
	})
	assertProvisionSucceeded(t, output)
	for _, want := range []string{"image cosign signature verified", "sysext cosign signature verified", "method=keyless"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output lacks %q. tail:\n%s", want, tail(output, 3000))
		}
	}

	root, cleanup := mountQcow2Partition(t, targetDisk, 2)
	defer cleanup()
	if got := strings.TrimSpace(readProvisionedFile(t, root, "etc/hostname")); got != "keyless-signed" {
		t.Fatalf("hostname = %q", got)
	}
	assertLayeredLayoutJSON(t, root)
	if got := readProvisionedFile(t, root, "usr/lib/tcaas-sysext/preloaded/node-tuning.raw"); got != string(sysextPayload) {
		t.Fatalf("preloaded sysext = %q", got)
	}
}

// TestKeylessCosignOtherIdentityRefusedVM signs the image with another
// identity of the same Sigstore: BOOTy must stop in verify-image, before
// the disk is wiped.
func TestKeylessCosignOtherIdentityRefusedVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)

	ss := newKeylessTestSigstore(t)
	pushHost, guestHost := startABOCIRegistry(t)
	imageDigest := pushCosignTestImage(t, pushHost, "test/layered-os",
		static.NewLayer(readFile(t, compressGzip(t, createLayeredLayoutTestDiskImage(t, 512))), types.OCILayer))
	signKeylessBundle(t, ss, pushHost+"/test/layered-os@"+imageDigest, "https://ci.example/someone/else//.ci/build.yml@refs/heads/main")

	output, targetDisk := runKeylessProvision(t, ss, "keyless-refused", "oci://"+guestHost+"/test/layered-os@"+imageDigest, nil)
	text := string(output)
	if !strings.Contains(text, "step=verify-image") || !strings.Contains(text, "does not match the identity regexp") {
		t.Fatalf("no keyless refusal in verify-image. tail:\n%s", tail(output, 3000))
	}
	for _, step := range []string{"step=wipe-disks", "step=stream-image"} {
		if strings.Contains(text, step) {
			t.Fatalf("%s ran after the refused signature. tail:\n%s", step, tail(output, 3000))
		}
	}
	run(t, "check the target disk marker", "qemu-io", "-f", "qcow2", "-c", "read -P 0xab 0 1M", targetDisk)
}

func newKeylessTestSigstore(t *testing.T) *sigstoretest.Sigstore {
	t.Helper()
	ss, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func signKeylessBundle(t *testing.T, ss *sigstoretest.Sigstore, ref, identity string) {
	t.Helper()
	d, err := name.NewDigest(ref)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ss.Bundle(d.DigestStr(), sigstoretest.Options{Identity: identity, Issuer: keylessIssuer})
	if err != nil {
		t.Fatal(err)
	}
	if err := sigstoretest.PushBundle(d, b); err != nil {
		t.Fatalf("push bundle referrer: %v", err)
	}
}

func runKeylessProvision(t *testing.T, ss *sigstoretest.Sigstore, hostname, imageURL string, extra map[string]string) (output []byte, targetDisk string) {
	t.Helper()
	rootJSON, err := ss.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{
		"HOSTNAME":                     hostname,
		"MODE":                         "provision",
		"DISK_DEVICE":                  "/dev/vda",
		"IMAGE":                        imageURL,
		"IMAGE_COSIGN_TRUSTED_ROOT":    "/" + keylessTrustedRootPath,
		"IMAGE_COSIGN_IDENTITY_REGEXP": `https://ci\.example/os/image//\.ci/build\.yml@refs/heads/main`,
		"IMAGE_COSIGN_OIDC_ISSUER":     keylessIssuer,
	}
	for k, v := range extra {
		vars[k] = v
	}
	initramfs := buildProvisionInitramfsWithFiles(t, vars, map[string]string{keylessTrustedRootPath: string(rootJSON)})
	targetDisk = filepath.Join(t.TempDir(), hostname+".qcow2")
	run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")
	run(t, "mark the target disk", "qemu-io", "-f", "qcow2", "-c", "write -P 0xab 0 1M", targetDisk)
	output = runQEMUProvision(t, findKernel(t), initramfs, targetDisk, 7*time.Minute)
	t.Logf("%s VM output tail:\n%s", hostname, tail(output, 3000))
	return output, targetDisk
}
