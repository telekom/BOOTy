package image

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// faultServer serves data like a static file server (Range, If-Range,
// ETag). The first cuts full GETs or ranged GETs break the connection after
// half of the response; etag returns the ETag of each request.
type faultServer struct {
	data     []byte
	cuts     atomic.Int32
	requests atomic.Int32
	etag     func(n int32) string
	// status, when set, answers every ranged GET.
	status int
	// grown, when set, is served from the second request on.
	grown []byte
}

func (s *faultServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := s.requests.Add(1)
	if s.status != 0 && r.Header.Get("Range") != "" {
		w.WriteHeader(s.status)
		return
	}
	data := s.data
	if s.grown != nil && n > 1 {
		data = s.grown
	}
	tag := `"v1"`
	if s.etag != nil {
		tag = s.etag(n)
	}
	if tag != "" {
		w.Header().Set("ETag", tag)
	}
	if s.cuts.Add(-1) >= 0 {
		// Serve the response into a buffer and send only its first half.
		rec := httptest.NewRecorder()
		rec.Header().Set("ETag", tag)
		http.ServeContent(rec, r, "image.raw", time.Time{}, bytes.NewReader(data))
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		body := rec.Body.Bytes()
		_, _ = w.Write(body[:len(body)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	http.ServeContent(w, r, "image.raw", time.Time{}, bytes.NewReader(data))
}

func testPayload(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i * 7)
	}
	return p
}

func withDownloadOptions(t *testing.T, o DownloadOptions) {
	t.Helper()
	origBackoff := retryBackoffBase
	retryBackoffBase = time.Millisecond
	SetDownloadOptions(o)
	t.Cleanup(func() {
		retryBackoffBase = origBackoff
		SetDownloadOptions(DownloadOptions{})
	})
}

// changesAfter returns an ETag function of an image that changes after the
// first k requests.
func changesAfter(k int32) func(int32) string {
	return func(n int32) string {
		if n <= k {
			return `"v1"`
		}
		return `"v2"`
	}
}

func TestHTTPDownloadResume(t *testing.T) {
	data := testPayload(1 << 20)
	tests := []struct {
		name    string
		opts    DownloadOptions
		cuts    int32
		etag    func(n int32) string
		status  int
		grown   bool
		wantErr error // nil: data arrives complete
		// maxRequests, when set, bounds the requests of the download.
		maxRequests int32
	}{
		{name: "resumes after two breaks", opts: DownloadOptions{Resume: true}, cuts: 2},
		{name: "without resume a break fails", cuts: 1, wantErr: io.ErrUnexpectedEOF},
		{
			name: "image changed on the server", opts: DownloadOptions{Resume: true}, cuts: 1,
			etag:    changesAfter(1),
			wantErr: errImageChanged,
		},
		{
			name: "no validator: no resume", opts: DownloadOptions{Resume: true}, cuts: 1,
			etag:    func(int32) string { return "" },
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name: "weak ETag: no resume", opts: DownloadOptions{Resume: true}, cuts: 1,
			etag:    func(int32) string { return `W/"v1"` },
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name: "same ETag but another size", opts: DownloadOptions{Resume: true}, cuts: 1, grown: true,
			wantErr: errImageChanged,
		},
		{
			name: "a client error ends the resume", opts: DownloadOptions{Resume: true}, cuts: 1, status: http.StatusForbidden,
			wantErr: errRangeRejected, maxRequests: 2,
		},
		{name: "parallel ranges", opts: DownloadOptions{ParallelRanges: 3}},
		{name: "parallel ranges retry a broken range", opts: DownloadOptions{ParallelRanges: 3}, cuts: 3},
		{
			name: "parallel ranges of a changed image", opts: DownloadOptions{ParallelRanges: 3},
			etag:    changesAfter(2),
			wantErr: errImageChanged,
		},
	}
	origChunk := rangeChunkSize
	rangeChunkSize = 100 << 10
	t.Cleanup(func() { rangeChunkSize = origChunk })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withDownloadOptions(t, tt.opts)
			fs := &faultServer{data: data, etag: tt.etag, status: tt.status}
			if tt.grown {
				fs.grown = append(bytes.Clone(data), 0)
			}
			fs.cuts.Store(tt.cuts)
			srv := httptest.NewServer(fs)
			defer srv.Close()

			body, err := httpGetWithRetry(context.Background(), srv.URL+"/image.raw")
			if err != nil {
				t.Fatalf("httpGetWithRetry() = %v", err)
			}
			got, err := io.ReadAll(body)
			_ = body.Close()
			if tt.maxRequests > 0 && fs.requests.Load() > tt.maxRequests {
				t.Errorf("%d requests, want at most %d", fs.requests.Load(), tt.maxRequests)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ReadAll() = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadAll() = %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("got %d bytes that differ from the %d byte image", len(got), len(data))
			}
		})
	}
}

// TestRangeReaderCloseStopsWorkers closes a parallel download in the middle:
// the workers must stop.
func TestRangeReaderCloseStopsWorkers(t *testing.T) {
	withDownloadOptions(t, DownloadOptions{})
	origChunk := rangeChunkSize
	rangeChunkSize = 64 << 10
	t.Cleanup(func() { rangeChunkSize = origChunk })
	data := testPayload(4 << 20)
	srv := httptest.NewServer(&faultServer{data: data})
	defer srv.Close()
	before := runtime.NumGoroutine()
	r := newRangeReader(context.Background(), srv.URL+"/image.raw", `"v1"`, int64(len(data)), 4)
	if _, err := io.ReadFull(r, make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	srv.CloseClientConnections()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 { // the HTTP client may keep idle connections
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines after Close, %d before", runtime.NumGoroutine(), before)
		}
		runtime.Gosched()
	}
}
