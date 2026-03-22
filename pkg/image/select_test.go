package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSelectBestSource(t *testing.T) {
	t.Helper()

	tests := []struct {
		name    string
		urls    []string
		wantErr bool
	}{
		{
			name:    "empty urls",
			urls:    []string{},
			wantErr: true,
		},
		{
			name:    "single url returned as-is",
			urls:    []string{"http://example.com/image.raw"},
			wantErr: false,
		},
		{
			name:    "single oci url returned as-is",
			urls:    []string{"oci://registry.example.com/image:latest"},
			wantErr: false,
		},
		{
			name:    "all oci urls returns first",
			urls:    []string{"oci://a.example.com/img:v1", "oci://b.example.com/img:v1"},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectBestSource(context.Background(), tc.urls)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == "" {
				t.Fatal("expected non-empty URL")
			}
		})
	}
}

func TestSelectBestSourcePicksFastest(t *testing.T) {
	t.Helper()

	// Slow server — adds 200ms delay.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	// Fast server — responds immediately.
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fast.Close()

	got, err := SelectBestSource(context.Background(), []string{slow.URL + "/image.raw", fast.URL + "/image.raw"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != fast.URL+"/image.raw" {
		t.Errorf("expected fast URL %s, got %s", fast.URL+"/image.raw", got)
	}
}

func TestSelectBestSourceFallsBackOnFailure(t *testing.T) {
	t.Helper()

	// Broken server — returns 500.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	// Working server.
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer working.Close()

	got, err := SelectBestSource(context.Background(), []string{broken.URL + "/img", working.URL + "/img"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != working.URL+"/img" {
		t.Errorf("expected working URL %s, got %s", working.URL+"/img", got)
	}
}

func TestSelectBestSourceAllFailed(t *testing.T) {
	t.Helper()

	// Both servers return errors.
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s1.Close()
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s2.Close()

	// Should fall back to first URL when all probes fail.
	got, err := SelectBestSource(context.Background(), []string{s1.URL + "/img", s2.URL + "/img"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != s1.URL+"/img" {
		t.Errorf("expected first URL as fallback %s, got %s", s1.URL+"/img", got)
	}
}

func TestRedactHost(t *testing.T) {
	t.Helper()

	tests := []struct {
		input string
		want  string
	}{
		{"http://example.com/image.raw", "example.com"},
		{"https://user:pass@registry.io/img", "registry.io"},
		{"://invalid", "<invalid-url>"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := redactHost(tc.input)
			if got != tc.want {
				t.Errorf("redactHost(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
