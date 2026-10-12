package caprf

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestTransportErrorsKeepCancelAndTimeout(t *testing.T) {
	c := NewFromConfig(&config.MachineConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.doPost(ctx, leakURL, "{}"); !errors.Is(err, context.Canceled) {
		t.Fatalf("doPost with canceled context = %v, want context.Canceled", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	c.httpClient = &http.Client{Timeout: 50 * time.Millisecond}
	err := c.doPost(context.Background(), srv.URL+"/?X-Amz-Signature=SECRETSIG123", "{}")
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("doPost timeout = %v, want a net.Error with Timeout()", err)
	}
	if strings.Contains(err.Error(), "SECRETSIG123") {
		t.Fatalf("timeout error leaks the signature: %v", err)
	}
}

func TestTransportErrorsRedactRedirectTarget(t *testing.T) {
	for _, location := range []string{
		"http://127.0.0.1:1/target?X-Amz-Signature=REDIRECTSECRET",
		"http://127.0.0.1:1/%zz?X-Amz-Signature=REDIRECTSECRET", // malformed Location
		"/%zz?X-Amz-Signature=REDIRECTSECRET",                   // malformed relative Location
		"/%zz#token=REDIRECTSECRET",                             // malformed relative Location with fragment
		"//user:REDIRECTSECRET@127.0.0.1:1/%zz",                 // malformed protocol-relative Location
		"/%zz?note='&X-Amz-Signature=REDIRECTSECRET",            // quote inside the query
		"//us'er:REDIRECTSECRET@127.0.0.1:1/%zz",                // quote inside the userinfo
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusFound)
		}))
		c := NewFromConfig(&config.MachineConfig{})
		err := c.doPost(context.Background(), srv.URL+"/start", "{}")
		srv.Close()
		if err == nil {
			t.Fatalf("%s: expected an error from the redirect target", location)
		}
		if strings.Contains(err.Error(), "REDIRECTSECRET") {
			t.Fatalf("redirect error leaks the signature: %v", err)
		}
	}
}
