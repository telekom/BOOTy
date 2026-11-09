package image

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type punchCall struct{ off, n int64 }

// fakePunch replaces punchZeroes: it records the calls and zeroes the range
// by writing, or fails with err.
func fakePunch(t *testing.T, err error) *[]punchCall {
	t.Helper()
	var calls []punchCall
	orig := punchZeroes
	punchZeroes = func(f *os.File, off, n int64) error {
		calls = append(calls, punchCall{off, n})
		if err != nil {
			return err
		}
		_, werr := f.WriteAt(make([]byte, n), off)
		return werr
	}
	t.Cleanup(func() { punchZeroes = orig })
	return &calls
}

// testImage is 6 blocks and a short tail: data, zero, zero, data, zero,
// data, then a 100-byte zero tail that is never punched.
func testImage() []byte {
	const b = writeBlockSize
	img := make([]byte, 6*b+100)
	for _, blk := range []int{0, 3, 5} {
		for i := blk * b; i < (blk+1)*b; i++ {
			img[i] = byte(i%251 + 1)
		}
	}
	return img
}

func targetFile(t *testing.T, content []byte) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestBlockWriter(t *testing.T) {
	const b = writeBlockSize
	img := testImage()
	stale := bytes.Repeat([]byte{0xee}, len(img))
	oneBlockChanged := bytes.Clone(img)
	oneBlockChanged[3*b+7] ^= 0xff

	tests := []struct {
		name          string
		opt           StreamOpts
		target        []byte
		punchErr      error
		wantPunches   []punchCall
		wantPunched   int64
		wantUnchanged int64
	}{
		{name: "dense", target: stale},
		{
			name:        "sparse punches each zero run once",
			opt:         StreamOpts{SparseWrite: true},
			target:      stale,
			wantPunches: []punchCall{{b, 2 * b}, {4 * b, b}},
			wantPunched: 3 * b,
		},
		{
			name:        "sparse writes zeros when the device cannot punch",
			opt:         StreamOpts{SparseWrite: true},
			target:      stale,
			punchErr:    errors.ErrUnsupported,
			wantPunches: []punchCall{{b, 2 * b}},
		},
		{
			name:          "skip unchanged writes only the changed block",
			opt:           StreamOpts{SkipUnchanged: true},
			target:        oneBlockChanged,
			wantUnchanged: int64(len(img)) - b,
		},
		{
			name:          "skip unchanged on a stale target writes everything",
			opt:           StreamOpts{SkipUnchanged: true},
			target:        stale,
			wantUnchanged: 0,
		},
		{
			name:          "sparse and skip unchanged",
			opt:           StreamOpts{SparseWrite: true, SkipUnchanged: true},
			target:        oneBlockChanged,
			wantPunches:   []punchCall{{b, 2 * b}, {4 * b, b}},
			wantPunched:   3 * b,
			wantUnchanged: 2*b + 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := fakePunch(t, tt.punchErr)
			f := targetFile(t, tt.target)
			w := newBlockWriter(f, tt.opt)
			// A reader that returns odd sizes checks that blocks stay
			// aligned to the device offset, not to the reads.
			n, err := w.copyFrom(io.LimitReader(&oddReader{r: bytes.NewReader(img)}, int64(len(img))))
			if err != nil {
				t.Fatalf("copyFrom() = %v", err)
			}
			if n != int64(len(img)) {
				t.Fatalf("copied %d bytes, want %d", n, len(img))
			}
			got, err := os.ReadFile(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, img) {
				t.Fatal("target does not hold the image")
			}
			if len(*calls) != len(tt.wantPunches) {
				t.Fatalf("punches = %v, want %v", *calls, tt.wantPunches)
			}
			for i := range *calls {
				if (*calls)[i] != tt.wantPunches[i] {
					t.Fatalf("punches = %v, want %v", *calls, tt.wantPunches)
				}
			}
			if w.punched != tt.wantPunched || w.unchanged != tt.wantUnchanged {
				t.Fatalf("punched %d unchanged %d, want %d and %d", w.punched, w.unchanged, tt.wantPunched, tt.wantUnchanged)
			}
		})
	}
}

// oddReader returns at most 3 bytes less than a block per Read.
type oddReader struct{ r io.Reader }

func (o *oddReader) Read(p []byte) (int, error) {
	return o.r.Read(p[:min(len(p), writeBlockSize-3)]) //nolint:wrapcheck // test reader
}

// TestWriteImageFailsOnTruncatedStream: a decompressor reports a truncated
// image as io.ErrUnexpectedEOF; the write must fail, not end early.
func TestWriteImageFailsOnTruncatedStream(t *testing.T) {
	f := targetFile(t, nil)
	src := io.MultiReader(bytes.NewReader([]byte("partial")), errReader{io.ErrUnexpectedEOF})
	if _, err := writeImage(f, src, StreamOpts{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("writeImage() = %v, want io.ErrUnexpectedEOF", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
