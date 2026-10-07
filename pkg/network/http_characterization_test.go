// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectivityResponseAndDiagnosticCharacterization(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			methods := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods <- r.Method
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			target, err := url.Parse(server.URL + "/node.img")
			if err != nil {
				t.Fatal(err)
			}
			target.User = url.UserPassword("fixture-user", "fixture-password")
			target.RawQuery, target.Fragment = "token=fixture-token", "fixture-fragment"
			if err := WaitForHTTP(t.Context(), target.String(), time.Second); err != nil {
				t.Fatalf("HTTP response must establish connectivity: %v", err)
			}
			select {
			case method := <-methods:
				if method != http.MethodHead {
					t.Fatalf("method = %s, want HEAD", method)
				}
			case <-t.Context().Done():
				t.Fatal("connectivity request not observed")
			}
			for _, secret := range []string{"fixture-user", "fixture-password", "fixture-token", "fixture-fragment"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("diagnostic leaked %q: %s", secret, &logs)
				}
			}
			if !strings.Contains(logs.String(), server.URL+"/node.img") {
				t.Fatalf("diagnostic lost target context: %s", &logs)
			}
		})
	}
}

func TestConnectivityCanceledBeforeRequestCharacterization(t *testing.T) {
	var requested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requested.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WaitForHTTP(ctx, server.URL, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("connectivity error = %v, want context.Canceled", err)
	}
	if requested.Load() {
		t.Fatal("canceled connectivity check made a request")
	}
}
