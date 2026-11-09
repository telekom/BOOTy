package image

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
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
		p[i] = byte(i*7 + i>>8 + i>>16) // distinct per 256-byte period: swapped ranges differ
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

// TestFetchRangeEmptyBody: an empty 206 body is a short range, not the end
// of the image; readers above take a plain io.EOF as the end.
func TestFetchRangeEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-99/100")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer srv.Close()
	c := fetchRange(t.Context(), srv.URL, `"v1"`, 0, 99, 100, time.Millisecond)
	if !errors.Is(c.err, io.ErrUnexpectedEOF) {
		t.Fatalf("fetchRange() error = %v, want io.ErrUnexpectedEOF", c.err)
	}
}

// TestResumeNeedsContentLength: without a Content-Length a resumed body
// could end early unnoticed, so the download does not resume.
func TestResumeNeedsContentLength(t *testing.T) {
	withDownloadOptions(t, DownloadOptions{Resume: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("chunked"))
		w.(http.Flusher).Flush() // no Content-Length: chunked
	}))
	defer srv.Close()
	body, err := httpGetWithRetry(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if _, ok := body.(*resumingBody); ok {
		t.Fatal("a body of unknown length resumes")
	}
}

// TestRangeReaderFailsFast: a range that fails for good cancels the others,
// so a stalled earlier range does not delay the error.
func TestRangeReaderFailsFast(t *testing.T) {
	withDownloadOptions(t, DownloadOptions{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Range"), "bytes=0-") {
			<-r.Context().Done() // the first range stalls
			return
		}
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()
	r := newRangeReader(t.Context(), srv.URL, `"v1"`, 4*rangeChunkSize, 3)
	defer func() { _ = r.Close() }()
	done := make(chan error, 1)
	go func() { _, err := r.Read(make([]byte, 1)); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, errImageChanged) {
			t.Fatalf("Read() = %v, want errImageChanged", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Read() waits for the stalled range after another range failed for good")
	}
}

// TestRangeReaderParentCancel: a canceled parent ctx stops the download
// early; Read must report it, not end the image with io.EOF.
func TestRangeReaderParentCancel(t *testing.T) {
	withDownloadOptions(t, DownloadOptions{})
	origChunk := rangeChunkSize
	rangeChunkSize = 64 << 10
	t.Cleanup(func() { rangeChunkSize = origChunk })
	data := testPayload(1 << 20)
	srv := httptest.NewServer(&faultServer{data: data})
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	r := newRangeReader(ctx, srv.URL+"/image.raw", `"v1"`, int64(len(data)), 3)
	defer func() { _ = r.Close() }()
	if _, err := io.ReadFull(r, make([]byte, rangeChunkSize)); err != nil {
		t.Fatal(err)
	}
	cancel()
	got, err := io.ReadAll(r)
	if err == nil {
		t.Fatalf("ReadAll() after cancel = %d bytes and nil, want an error", rangeChunkSize+int64(len(got)))
	}
}

// hangUpServer closes every connection before it sends a response, which
// the client reports as a wrapped io.EOF.
func hangUpServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRangeFailuresAreNotEOF: a range or a resume that fails on an empty
// body or a connection closed before the response must not end in io.EOF,
// which readers above take as the end of the image.
func TestRangeFailuresAreNotEOF(t *testing.T) {
	withDownloadOptions(t, DownloadOptions{})
	srv := hangUpServer(t)
	c := fetchRange(t.Context(), srv.URL, `"v1"`, 0, 99, 100, time.Millisecond)
	if c.err == nil || errors.Is(c.err, io.EOF) || !errors.Is(c.err, io.ErrUnexpectedEOF) {
		t.Errorf("fetchRange() error = %v, want io.ErrUnexpectedEOF and not io.EOF", c.err)
	}
	b := &resumingBody{ctx: t.Context(), url: srv.URL, etag: `"v1"`, size: 100, body: io.NopCloser(strings.NewReader("short"))}
	_, err := io.ReadAll(b)
	if err == nil || errors.Is(err, io.EOF) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("resumingBody error = %v, want io.ErrUnexpectedEOF and not io.EOF", err)
	}
}

// TestRangesFollowTheRedirect: ranges go to the resource that answered the
// first GET. The redirect then points to another image with the same ETag
// and size, which must not be mixed in.
func TestRangesFollowTheRedirect(t *testing.T) {
	origChunk := rangeChunkSize
	rangeChunkSize = 64 << 10
	t.Cleanup(func() { rangeChunkSize = origChunk })
	v1, v2 := testPayload(1<<20), bytes.Repeat([]byte{0xee}, 1<<20)
	for _, opts := range []DownloadOptions{{Resume: true}, {ParallelRanges: 3}} {
		withDownloadOptions(t, opts)
		var redirects atomic.Int32
		srv1 := &faultServer{data: v1}
		srv1.cuts.Store(1) // the first GET breaks, so Resume requests a range
		mux := http.NewServeMux()
		mux.Handle("/v1", srv1)
		mux.Handle("/v2", &faultServer{data: v2})
		mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
			target := "/v2"
			if redirects.Add(1) == 1 {
				target = "/v1"
			}
			http.Redirect(w, r, target, http.StatusFound)
		})
		srv := httptest.NewServer(mux)
		body, err := httpGetWithRetry(t.Context(), srv.URL+"/latest")
		if err != nil {
			t.Fatalf("%+v: httpGetWithRetry() = %v", opts, err)
		}
		got, err := io.ReadAll(body)
		_ = body.Close()
		srv.Close()
		if err != nil || !bytes.Equal(got, v1) {
			t.Fatalf("%+v: ReadAll() = %d bytes, %v; want the %d bytes of /v1", opts, len(got), err, len(v1))
		}
	}
}
