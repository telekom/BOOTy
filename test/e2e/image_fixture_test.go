//go:build e2e && linux

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func startE2ERawImageServer(t *testing.T) string {
	t.Helper()

	payload := []byte("booty e2e raw image fixture\n")
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		_, _ = w.Write(payload)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server.URL + "/test.img"
}
