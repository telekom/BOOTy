//go:build linux

package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/image"
)

// A probe that keeps failing with a transient error stops the run before
// the wipe with the format probe code; one that recovers lets it go on.
func TestImageProbeFailsClosedBeforeTheWipe(t *testing.T) {
	origProbe, origDelay := validateStreamingPrerequisites, probeRetryDelay
	t.Cleanup(func() { validateStreamingPrerequisites, probeRetryDelay = origProbe, origDelay })
	probeRetryDelay = time.Millisecond
	transient := fmt.Errorf("%w: %w", image.ErrFormatProbe, context.DeadlineExceeded)

	for _, tt := range []struct {
		name     string
		failures int
		wantErr  bool
	}{
		{name: "transient error every time", failures: probeAttempts, wantErr: true},
		{name: "transient error, then the probe works", failures: probeAttempts - 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			validateStreamingPrerequisites = func(context.Context, string) (image.Format, error) {
				calls++
				if calls <= tt.failures {
					return image.FormatRaw, transient
				}
				return image.FormatRaw, nil
			}
			o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
			o.bestImageURL = "https://img.example/os.img"
			err := o.validateImageStreamingPrerequisites(context.Background(), []string{o.bestImageURL})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if code := CodeOf(WithCode("validate-provision-inputs", err)); code != codeFormatProbe {
					t.Fatalf("code = %q, want %s", code, codeFormatProbe)
				}
				if calls != probeAttempts {
					t.Fatalf("probe calls = %d, want %d", calls, probeAttempts)
				}
			}
		})
	}
}

// IMAGE_STREAM fails a qcow2 source before the wipe, with a permanent code;
// without it the qcow2 is staged as before.
func TestImageProbeRejectsQCOW2WithImageStream(t *testing.T) {
	orig := validateStreamingPrerequisites
	t.Cleanup(func() { validateStreamingPrerequisites = orig })
	validateStreamingPrerequisites = func(context.Context, string) (image.Format, error) {
		return image.FormatQCOW2, nil
	}
	for _, stream := range []bool{false, true} {
		cfg := &config.MachineConfig{}
		cfg.Provision.Image.Stream = stream
		o := newTestOrchestrator(t, cfg, &mockProvider{})
		o.bestImageURL = "https://img.example/os.qcow2"
		err := o.validateImageStreamingPrerequisites(context.Background(), []string{o.bestImageURL})
		if !stream {
			if err != nil {
				t.Fatalf("without IMAGE_STREAM: %v", err)
			}
			continue
		}
		if !errors.Is(err, image.ErrStagingForbidden) {
			t.Fatalf("with IMAGE_STREAM: err = %v, want ErrStagingForbidden", err)
		}
		if code := CodeOf(WithCode("validate-provision-inputs", err)); code != codeFormatProbe {
			t.Fatalf("code = %q, want %s", code, codeFormatProbe)
		}
		var permanent *PermanentError
		if !errors.As(classifyImageStreamError("https://img.example/os.qcow2", err), &permanent) {
			t.Fatal("a staging error of stream-image is retried")
		}
	}
}

// With IMAGE_STREAM an oci:// source is probed before the wipe, so a qcow2
// layer fails there; without it the probe skips OCI sources as before.
func TestImageProbeOCIWithImageStream(t *testing.T) {
	orig := validateStreamingPrerequisites
	t.Cleanup(func() { validateStreamingPrerequisites = orig })
	for _, stream := range []bool{false, true} {
		probed := false
		validateStreamingPrerequisites = func(context.Context, string) (image.Format, error) {
			probed = true
			return image.FormatQCOW2, nil
		}
		cfg := &config.MachineConfig{}
		cfg.Provision.Image.Stream = stream
		o := newTestOrchestrator(t, cfg, &mockProvider{})
		o.bestImageURL = "oci://registry.example/node-image@sha256:" + strings.Repeat("a", 64)
		err := o.validateImageStreamingPrerequisites(context.Background(), []string{o.bestImageURL})
		if probed != stream {
			t.Fatalf("IMAGE_STREAM=%v: probed = %v", stream, probed)
		}
		if stream != errors.Is(err, image.ErrStagingForbidden) {
			t.Fatalf("IMAGE_STREAM=%v: err = %v", stream, err)
		}
	}
}

// A staged image that does not fit is not retried; another stream-image
// error gets the three retries of the policy.
func TestStreamImageRetries(t *testing.T) {
	policy := DefaultPolicies["stream-image"]
	policy.InitialDelay, policy.MaxDelay = 0, 0
	for _, tt := range []struct {
		err      error
		attempts int
	}{
		{errors.New("connection reset by peer"), 4},
		{fmt.Errorf("writing qcow2 image: %w", image.ErrStagingFull), 1},
	} {
		attempts := 0
		err := WithRetry(context.Background(), "stream-image", policy, func(context.Context) error {
			attempts++
			return classifyImageStreamError("https://img.example/os.img", tt.err)
		})
		if err == nil || attempts != tt.attempts {
			t.Fatalf("%v: %d attempts (err %v), want %d", tt.err, attempts, err, tt.attempts)
		}
	}
}
