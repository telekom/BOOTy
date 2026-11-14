package image

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The image client built during package initialization follows redirects,
// the documented default, without a RefreshHTTPClients call.
func TestInitialImageClientFollowsRedirects(t *testing.T) {
	if _, wrapped := imageHTTPClient.Transport.(redirectRefuser); wrapped {
		t.Fatal("initial image client refuses redirects")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "image")
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()
	body, err := httpGetWithRetry(context.Background(), redirector.URL)
	if err != nil {
		t.Fatalf("GET = %v, want the redirect followed", err)
	}
	_ = body.Close()
}

// net/http returns a redirect status without Location as a plain response
// and never asks CheckRedirect. IMAGE_FOLLOW_REDIRECTS=false refuses it as
// well, so the source probe does not pick such a source.
func TestImageRefusesRedirectWithoutLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	t.Cleanup(func() {
		SetFollowRedirects(true)
		RefreshHTTPClients()
	})
	SetFollowRedirects(false)
	RefreshHTTPClients()
	ctx := context.Background()
	if _, err := probeURL(ctx, srv.URL); !errors.Is(err, ErrRedirectRefused) || !strings.Contains(err.Error(), "status 302 without Location") {
		t.Fatalf("probe = %v, want ErrRedirectRefused for a 302 without Location", err)
	}
	if _, err := httpGetWithRetry(ctx, srv.URL); !errors.Is(err, ErrRedirectRefused) {
		t.Fatalf("GET = %v, want ErrRedirectRefused", err)
	}
}

// IMAGE_FOLLOW_REDIRECTS=false: a redirect fails the download, the probe
// and the signature download at once, the target gets no request and the
// error redacts the location. The default follows.
func TestImageFollowRedirects(t *testing.T) {
	var targetHits, redirectHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = io.WriteString(w, "image")
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHits.Add(1)
		http.Redirect(w, r, target.URL+"/image?sig=secret", http.StatusFound)
	}))
	defer redirector.Close()
	oldBackoff := retryBackoffBase
	retryBackoffBase = time.Millisecond
	t.Cleanup(func() {
		retryBackoffBase = oldBackoff
		SetFollowRedirects(true)
		RefreshHTTPClients()
	})
	ctx := context.Background()

	SetFollowRedirects(true)
	RefreshHTTPClients()
	body, err := httpGetWithRetry(ctx, redirector.URL)
	if err != nil {
		t.Fatalf("default: GET = %v, want the redirect followed", err)
	}
	_ = body.Close()
	if targetHits.Load() != 1 {
		t.Fatalf("default: target hits = %d, want 1", targetHits.Load())
	}

	SetFollowRedirects(false)
	RefreshHTTPClients()
	targetHits.Store(0)
	redirectHits.Store(0)
	_, err = httpGetWithRetry(ctx, redirector.URL)
	if !errors.Is(err, ErrRedirectRefused) {
		t.Fatalf("refused: GET = %v, want ErrRedirectRefused", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "status 302") || strings.Contains(msg, "secret") {
		t.Fatalf("refused: error %q must name the status and redact the location", msg)
	}
	if n := redirectHits.Load(); n != 1 {
		t.Fatalf("refused: redirector hits = %d, want 1 (no retry)", n)
	}
	if _, err := probeURL(ctx, redirector.URL); !errors.Is(err, ErrRedirectRefused) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("refused: probe = %v, want ErrRedirectRefused without the query", err)
	}
	if _, err := downloadToTemp(ctx, redirector.URL, "sig-*"); !errors.Is(err, ErrRedirectRefused) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("refused: signature download = %v, want ErrRedirectRefused without the query", err)
	}
	if n := targetHits.Load(); n != 0 {
		t.Fatalf("refused: target hits = %d, want 0", n)
	}
}
