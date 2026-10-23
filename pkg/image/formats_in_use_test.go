//go:build linux

package image

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

// bzip2Fixture is `bzip2` of bzip2FixtureData (the Go stdlib has no bzip2
// encoder).
const (
	bzip2FixtureData = "bzip2 image content for testing\n"
	bzip2FixtureHex  = "425a6839314159265359ab324fb1000003d9800010400010003ba3dc102000314d32313131088646d407a2078b93d701d2ded5964a720c32b6504cfc5dc914e14242acc93ec4"
)

// testImageData returns compressible data with a few MiB, larger than the
// buffers of the decoders.
func testImageData() []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < 3<<20; i++ {
		b.WriteString("block ")
		b.WriteByte(byte(i))
		b.WriteByte(byte(i >> 8))
	}
	return b.Bytes()
}

// halves splits data so that a test can write two frames or members.
func halves(data []byte) [][]byte { return [][]byte{data[:len(data)/2], data[len(data)/2:]} }

func encodeGzipMembers(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, part := range halves(data) { // pigz and `cat a.gz b.gz` write several members
		w := gzip.NewWriter(&out)
		if _, err := w.Write(part); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func encodeZstdFrames(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, part := range halves(data) { // pzstd and `zstd -T0 --long` can write several frames
		w, err := zstd.NewWriter(&out, zstd.WithEncoderLevel(zstd.SpeedFastest))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(part); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

// encodeLZ4 writes lz4 frames like the T-CaaS image builder
// (`lz4 -6 --favor-decSpeed --no-frame-crc`): no content checksum.
func encodeLZ4(t *testing.T, data []byte, frames int) []byte {
	t.Helper()
	var out bytes.Buffer
	parts := [][]byte{data}
	if frames == 2 {
		parts = halves(data)
	}
	for _, part := range parts {
		w := lz4.NewWriter(&out)
		if err := w.Apply(lz4.CompressionLevelOption(lz4.Level6), lz4.ChecksumOption(false)); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(part); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func encodeXZ(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := xz.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestStreamFormatsInUse writes every image format that T-CaaS publishes or
// that BOOTy accepts, with the download checksum of the served file, with
// and without IMAGE_STREAM. Detection is by magic: every URL ends in .img.
func TestStreamFormatsInUse(t *testing.T) {
	data := testImageData()
	bz2, err := hex.DecodeString(bzip2FixtureHex)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body []byte
		want []byte
	}{
		{"raw", data, data},
		{"gzip two members", encodeGzipMembers(t, data), data},
		{"zstd two frames", encodeZstdFrames(t, data), data},
		{"lz4 no frame crc", encodeLZ4(t, data, 1), data},
		{"lz4 two frames", encodeLZ4(t, data, 2), data},
		{"xz", encodeXZ(t, data), data},
		{"bzip2", bz2, []byte(bzip2FixtureData)},
	}
	for _, tt := range tests {
		for _, streamOnly := range []bool{false, true} {
			name := tt.name
			if streamOnly {
				name += " IMAGE_STREAM"
			}
			t.Run(name, func(t *testing.T) {
				url := serveBytes(t, tt.body)
				target := filepath.Join(t.TempDir(), "disk")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(tt.body)
				err := Stream(context.Background(), url+"/image.img", target, StreamOpts{
					Checksum:   hex.EncodeToString(sum[:]),
					StreamOnly: streamOnly,
				})
				if err != nil {
					t.Fatalf("Stream() = %v", err)
				}
				got, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, tt.want) {
					t.Fatalf("target has %d bytes, want the %d decompressed bytes", len(got), len(tt.want))
				}
			})
		}
	}
}

// TestStreamOnlyRejectsQCOW2BeforeWrite checks that IMAGE_STREAM fails a
// plain and an lz4-compressed qcow2 without staging or touching the target.
func TestStreamOnlyRejectsQCOW2BeforeWrite(t *testing.T) {
	qcow2 := append([]byte{0x51, 0x46, 0x49, 0xfb}, make([]byte, 1024)...)
	orig := convertQCOW2Hook
	convertQCOW2Hook = func(context.Context, io.Reader, string, string, StreamOpts) error {
		t.Error("qcow2 was staged with IMAGE_STREAM")
		return nil
	}
	t.Cleanup(func() { convertQCOW2Hook = orig })

	for name, body := range map[string][]byte{"qcow2": qcow2, "qcow2.lz4": encodeLZ4(t, qcow2, 1)} {
		t.Run(name, func(t *testing.T) {
			url := serveBytes(t, body)
			target := filepath.Join(t.TempDir(), "disk")
			if err := os.WriteFile(target, []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := Stream(context.Background(), url+"/image", target, StreamOpts{StreamOnly: true})
			if !errors.Is(err, ErrStagingForbidden) {
				t.Fatalf("Stream() = %v, want ErrStagingForbidden", err)
			}
			if got, _ := os.ReadFile(target); string(got) != "previous" {
				t.Fatalf("target changed to %q", got)
			}
			err = StreamAB(context.Background(), url+"/image", ABTargets{RootPartition: target}, StreamOpts{StreamOnly: true})
			if !errors.Is(err, ErrStagingForbidden) {
				t.Fatalf("StreamAB() = %v, want ErrStagingForbidden", err)
			}
		})
	}
}

func TestStreamOnlyRejectsPartitionMode(t *testing.T) {
	err := StreamPartitions(context.Background(), "http://127.0.0.1:1/image", "/dev/null", StreamOpts{StreamOnly: true})
	if !errors.Is(err, ErrStagingForbidden) {
		t.Fatalf("StreamPartitions() = %v, want ErrStagingForbidden", err)
	}
}

func TestWipeImageTargetZeroesStartAndEnd(t *testing.T) {
	const mib = 1 << 20
	target := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(target, bytes.Repeat([]byte{0xff}, 4*mib), 0o600); err != nil {
		t.Fatal(err)
	}
	wipeImageTarget(target)
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	zero := make([]byte, mib)
	if !bytes.Equal(got[:mib], zero) || !bytes.Equal(got[3*mib:], zero) {
		t.Fatal("first or last MiB not zeroed")
	}
	if !bytes.Equal(got[mib:3*mib], bytes.Repeat([]byte{0xff}, 2*mib)) {
		t.Fatal("the middle of the device changed")
	}
}

// BenchmarkStreamPeakHeap streams 1 GiB of decompressed image per format to
// /dev/null and reports the peak Go heap: the RAM a streamed write needs,
// independent of the image size.
//
//	go test -run '^$' -bench StreamPeakHeap -benchtime 1x ./pkg/image/
func BenchmarkStreamPeakHeap(b *testing.B) {
	const size = 1 << 30
	chunk := bytes.Repeat([]byte("BOOTy streamed image block "), 1<<15)
	encoders := map[string]func(io.Writer) io.WriteCloser{
		"raw": func(w io.Writer) io.WriteCloser { return nopWriteCloser{w} },
		"lz4": func(w io.Writer) io.WriteCloser { return lz4.NewWriter(w) },
		"zstd": func(w io.Writer) io.WriteCloser {
			z, _ := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest))
			return z
		},
		"gzip": func(w io.Writer) io.WriteCloser { g, _ := gzip.NewWriterLevel(w, gzip.BestSpeed); return g },
	}
	origSync := syncImageTarget
	syncImageTarget = func(fileSyncer, string) error { return nil }
	b.Cleanup(func() { syncImageTarget = origSync })

	for name, enc := range encoders {
		b.Run(name, func(b *testing.B) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				e := enc(w)
				for n := 0; n < size; n += len(chunk) {
					if _, err := e.Write(chunk); err != nil {
						return
					}
				}
				_ = e.Close()
			}))
			defer srv.Close()
			for b.Loop() {
				runtime.GC()
				peak := samplePeakHeap()
				if err := Stream(context.Background(), srv.URL+"/image", os.DevNull, StreamOpts{StreamOnly: true}); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(peak())/(1<<20), "peak-heap-MiB")
			}
		})
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// samplePeakHeap samples HeapInuse until the returned function is called,
// which returns the peak.
func samplePeakHeap() func() uint64 {
	done := make(chan struct{})
	result := make(chan uint64)
	go func() {
		var peak uint64
		var m runtime.MemStats
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			runtime.ReadMemStats(&m)
			peak = max(peak, m.HeapInuse)
			select {
			case <-done:
				result <- peak
				return
			case <-ticker.C:
			}
		}
	}()
	return func() uint64 { close(done); return <-result }
}
