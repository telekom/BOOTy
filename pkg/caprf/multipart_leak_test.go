package caprf

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/crash"
)

func TestCrashFormUploadClosesReaderWhenRequestIsNotSent(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "crash.tar.gz")
	// Larger than the pipe buffer, so the writer goroutine blocks until the
	// reader is closed.
	if err := os.WriteFile(archive, make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewFromConfig(&config.MachineConfig{})
	// Bearer auth is only allowed for proxy uploads, so applyCrashUploadAuth
	// fails and the request is never sent.
	instructions := &crash.PrepareResponse{
		UploadURL:  "https://example.invalid/upload",
		UploadMode: crash.UploadModePresignedPOST,
		AuthMode:   crash.AuthModeBearer,
	}

	before := runtime.NumGoroutine()
	if err := c.uploadCrashPresignedForm(context.Background(), instructions, archive); err == nil {
		t.Fatal("expected an auth mode error")
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want <= %d: multipart writer leaked", runtime.NumGoroutine(), before)
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
}
