package image

import (
	"bytes"
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// benchTarget is the block device that BenchmarkStreamZstd writes to:
//
//	go test -c ./pkg/image && sudo ./image.test -test.run '^$' -test.bench StreamZstd -image.bench-target /dev/loop0
var benchTarget = flag.String("image.bench-target", "", "block device for BenchmarkStreamZstd (default: a temporary file)")

// benchImageMiB is the size of the synthetic image of BenchmarkStreamZstd,
// BOOTY_BENCH_IMAGE_MIB overrides it.
const benchImageMiB = 256

// syntheticDiskImage returns a raw image of sizeMiB that looks like a mostly
// empty disk: 60 % of the MiB blocks are zero, the rest is executable code
// (the running test binary), which compresses and decodes like the files of
// an OS image.
func syntheticDiskImage(tb testing.TB, sizeMiB int) []byte {
	tb.Helper()
	const mib = 1 << 20
	exe, err := os.Executable()
	if err != nil {
		tb.Fatal(err)
	}
	code, err := os.ReadFile(exe) //nolint:gosec // the running test binary
	if err != nil {
		tb.Fatal(err)
	}
	raw := make([]byte, sizeMiB*mib)
	src := 0
	for blk := range sizeMiB {
		if blk%5 >= 2 { // 3 of 5 blocks stay zero
			continue
		}
		for i := blk * mib; i < (blk+1)*mib; {
			n := copy(raw[i:(blk+1)*mib], code[src:])
			i += n
			src = (src + n) % len(code)
		}
	}
	return raw
}

func zstdCompress(tb testing.TB, raw []byte) []byte {
	tb.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := zw.Write(raw); err != nil {
		tb.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// serveImage serves data with Range support, like a static file server.
func serveImage(tb testing.TB, data []byte) string {
	tb.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "image.raw.zst", time.Unix(0, 0), bytes.NewReader(data))
	}))
	tb.Cleanup(srv.Close)
	return srv.URL + "/image.raw.zst"
}

// BenchmarkStreamZstd streams a synthetic zstd disk image from a local HTTP
// server to -image.bench-target (a block device, default a file in a
// temporary directory) with each write option. Every iteration writes the
// same image to the same target, so from the second iteration on
// skip-unchanged finds the image in place. Run it with -benchtime=Nx; BOOTY_BENCH_IMAGE_MIB sets
// the size.
//
//	go test -run '^$' -bench StreamZstd -benchtime 3x ./pkg/image/
func BenchmarkStreamZstd(b *testing.B) {
	sizeMiB := benchImageMiB
	if v, err := strconv.Atoi(os.Getenv("BOOTY_BENCH_IMAGE_MIB")); err == nil && v > 0 {
		sizeMiB = v
	}
	raw := syntheticDiskImage(b, sizeMiB)
	url := serveImage(b, zstdCompress(b, raw))
	target := *benchTarget
	if target == "" {
		target = filepath.Join(b.TempDir(), "target.img")
		if err := os.WriteFile(target, make([]byte, len(raw)), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for _, bc := range []struct {
		name string
		opt  StreamOpts
	}{
		{"dense", StreamOpts{}},
		{"sparse", StreamOpts{SparseWrite: true}},
		{"skip-unchanged", StreamOpts{SkipUnchanged: true}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.SetBytes(int64(len(raw)))
			for b.Loop() {
				if err := Stream(context.Background(), url, target, bc.opt); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
