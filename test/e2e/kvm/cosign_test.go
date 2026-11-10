//go:build e2e

package kvm

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"

	imageutil "github.com/telekom/BOOTy/pkg/image"
)

// Scenario cosign-verify (.github/workflows/kvm-matrix.yml) runs ^TestCosign:
// the layered-layout fixture as an oci:// image with IMAGE_COSIGN_PUBKEYS,
// signed (with a signed OCI sysext) and unsigned.

const cosignTestKeyPath = "deploy/cosign.pub"

// TestCosignSignedOCIImageVM provisions the fixture from a digest-pinned,
// signed OCI image and preloads a signed OCI sysext; both are verified
// against the configured key before the wipe.
func TestCosignSignedOCIImageVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	key := newCosignTestKey(t)
	pushHost, guestHost := startABOCIRegistry(t)
	imageDigest := pushCosignTestImage(t, pushHost, "test/layered-os",
		static.NewLayer(readFile(t, compressGzip(t, createLayeredLayoutTestDiskImage(t, 512))), types.OCILayer))
	signCosignTestImage(t, pushHost, "test/layered-os", imageDigest, key)
	sysextPayload := []byte("signed sysext vm e2e\n")
	sysextDigest := pushCosignTestImage(t, pushHost, "test/node-tuning",
		stream.NewLayer(io.NopCloser(bytes.NewReader(sysextPayload)), stream.WithMediaType(imageutil.SystemdSysextMediaType)))
	signCosignTestImage(t, pushHost, "test/node-tuning", sysextDigest, key)

	sysextLayers := fmt.Sprintf(`[{"name":"node-tuning","version":"vm-e2e","source":%q,"fileName":"node-tuning.raw","mode":"preload"}]`,
		"oci://"+guestHost+"/test/node-tuning@"+sysextDigest)
	output, targetDisk := runCosignProvision(t, "cosign-signed", "oci://"+guestHost+"/test/layered-os@"+imageDigest, key, map[string]string{
		"SYSEXT_ENABLED":        "true",
		"SYSEXT_DEFAULT_MODE":   "preload",
		"SYSEXT_LAYERS":         sysextLayers,
		"SYSEXT_COSIGN_PUBKEYS": "/" + cosignTestKeyPath,
	})
	assertProvisionSucceeded(t, output)
	for _, want := range []string{"image cosign signature verified", "sysext cosign signature verified"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output lacks %q. tail:\n%s", want, tail(output, 3000))
		}
	}

	root, cleanup := mountQcow2Partition(t, targetDisk, 2)
	defer cleanup()
	if got := strings.TrimSpace(readProvisionedFile(t, root, "etc/hostname")); got != "cosign-signed" {
		t.Fatalf("hostname = %q", got)
	}
	assertLayeredLayoutJSON(t, root)
	if got := readProvisionedFile(t, root, "usr/lib/tcaas-sysext/preloaded/node-tuning.raw"); got != string(sysextPayload) {
		t.Fatalf("preloaded sysext = %q", got)
	}
	var report struct {
		Verification struct {
			Signature struct {
				Method string `json:"method"`
				Result string `json:"result"`
			} `json:"signature"`
		} `json:"verification"`
	}
	data := readProvisionedFile(t, root, "etc/booty/provision.json")
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		t.Fatalf("decode provisioning report: %v\n%s", err, data)
	}
	if sig := report.Verification.Signature; sig.Method != "cosign" || sig.Result != "verified" {
		t.Fatalf("report signature = %+v, want cosign verified", sig)
	}
}

// TestCosignUnsignedOCIImageRefusedVM provisions the same fixture without a
// signature: BOOTy must stop in verify-image, before the disk is wiped.
func TestCosignUnsignedOCIImageRefusedVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)

	key := newCosignTestKey(t)
	pushHost, guestHost := startABOCIRegistry(t)
	imageDigest := pushCosignTestImage(t, pushHost, "test/layered-os",
		static.NewLayer(readFile(t, compressGzip(t, createLayeredLayoutTestDiskImage(t, 512))), types.OCILayer))

	output, targetDisk := runCosignProvision(t, "cosign-unsigned", "oci://"+guestHost+"/test/layered-os@"+imageDigest, key, nil)
	text := string(output)
	if !strings.Contains(text, "step=verify-image") || !strings.Contains(text, "cosign signature verification failed") {
		t.Fatalf("no cosign refusal in verify-image. tail:\n%s", tail(output, 3000))
	}
	for _, step := range []string{"step=wipe-disks", "step=stream-image"} {
		if strings.Contains(text, step) {
			t.Fatalf("%s ran after the refused signature. tail:\n%s", step, tail(output, 3000))
		}
	}
	// The marker that runCosignProvision wrote is still at the start of the
	// target disk, where any wipe or image write starts.
	run(t, "check the target disk marker", "qemu-io", "-f", "qcow2", "-c", "read -P 0xab 0 1M", targetDisk)
}

func runCosignProvision(t *testing.T, hostname, imageURL string, key *ecdsa.PrivateKey, extra map[string]string) (output []byte, targetDisk string) {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{
		"HOSTNAME":             hostname,
		"MODE":                 "provision",
		"DISK_DEVICE":          "/dev/vda",
		"IMAGE":                imageURL,
		"IMAGE_COSIGN_PUBKEYS": "/" + cosignTestKeyPath,
	}
	for k, v := range extra {
		vars[k] = v
	}
	initramfs := buildProvisionInitramfsWithFiles(t, vars, map[string]string{
		cosignTestKeyPath: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
	})
	targetDisk = filepath.Join(t.TempDir(), hostname+".qcow2")
	run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")
	run(t, "mark the target disk", "qemu-io", "-f", "qcow2", "-c", "write -P 0xab 0 1M", targetDisk)
	output = runQEMUProvision(t, findKernel(t), initramfs, targetDisk, 7*time.Minute)
	t.Logf("%s VM output tail:\n%s", hostname, tail(output, 3000))
	return output, targetDisk
}

func newCosignTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// pushCosignTestImage pushes a one-layer image and returns its digest.
func pushCosignTestImage(t *testing.T, host, repo string, layer v1.Layer) string {
	t.Helper()
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.NewTag(host + "/" + repo + ":v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	digest, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest.String()
}

// signCosignTestImage stores a cosign simple-signing signature of digest
// under its sha256-<hex>.sig tag, as `cosign sign --key` does.
func signCosignTestImage(t *testing.T, host, repo, digest string, key *ecdsa.PrivateKey) {
	t.Helper()
	payload := fmt.Appendf(nil,
		`{"critical":{"identity":{"docker-reference":%q},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"},"optional":null}`,
		host+"/"+repo, digest)
	h := sha256.Sum256(payload)
	sig, err := key.Sign(rand.Reader, h[:], crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.Append(empty.Image, mutate.Addendum{
		Layer:       static.NewLayer(payload, "application/vnd.dev.cosign.simplesigning.v1+json"),
		Annotations: map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(sig)},
	})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(host + "/" + repo + ":" + strings.Replace(digest, ":", "-", 1) + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatalf("push signature %s: %v", tag, err)
	}
}
