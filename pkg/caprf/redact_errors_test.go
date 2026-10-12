package caprf

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

const leakURL = "http://127.0.0.1:1/upload?X-Amz-Signature=SECRETSIG123"

func TestTransportErrorsRedactQueryCredentials(t *testing.T) {
	c := NewFromConfig(&config.MachineConfig{})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, leakURL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.doCrashUploadRequest(req, redactedURL(leakURL)); err == nil || strings.Contains(err.Error(), "SECRETSIG123") {
		t.Fatalf("crash upload error leaks or is nil: %v", err)
	}
	if err := c.doPost(context.Background(), leakURL, "{}"); err == nil || strings.Contains(err.Error(), "SECRETSIG123") {
		t.Fatalf("doPost error leaks or is nil: %v", err)
	}
}
