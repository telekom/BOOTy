package image

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

func TestDetectFormatGzip(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("hello"))
	_ = gz.Close()

	f, _, err := DetectFormat(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatGzip {
		t.Errorf("got %s, want %s", f, FormatGzip)
	}
}

func TestDetectFormatZstd(t *testing.T) {
	var buf bytes.Buffer
	w, _ := zstd.NewWriter(&buf)
	_, _ = w.Write([]byte("hello"))
	_ = w.Close()

	f, _, err := DetectFormat(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatZstd {
		t.Errorf("got %s, want %s", f, FormatZstd)
	}
}

func TestDetectFormatLZ4(t *testing.T) {
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	_, _ = w.Write([]byte("hello"))
	_ = w.Close()

	f, _, err := DetectFormat(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatLZ4 {
		t.Errorf("got %s, want %s", f, FormatLZ4)
	}
}

func TestDetectFormatXZ(t *testing.T) {
	var buf bytes.Buffer
	w, _ := xz.NewWriter(&buf)
	_, _ = w.Write([]byte("hello"))
	_ = w.Close()

	f, _, err := DetectFormat(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatXZ {
		t.Errorf("got %s, want %s", f, FormatXZ)
	}
}

func TestDetectFormatBzip2(t *testing.T) {
	// bzip2 magic: BZh (0x42 0x5a 0x68)
	data := []byte{0x42, 0x5a, 0x68, 0x39, 0x00, 0x00}
	f, _, err := DetectFormat(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatBzip2 {
		t.Errorf("got %s, want %s", f, FormatBzip2)
	}
}

func TestDetectFormatRaw(t *testing.T) {
	data := []byte("this is raw uncompressed data")
	f, _, err := DetectFormat(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatRaw {
		t.Errorf("got %s, want %s", f, FormatRaw)
	}
}

func TestDetectFormatShortData(t *testing.T) {
	data := []byte{0x01}
	f, _, err := DetectFormat(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatRaw {
		t.Errorf("got %s for short data, want %s", f, FormatRaw)
	}
}

func TestDecompressorGzip(t *testing.T) {
	want := "gzip test payload"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(want))
	_ = gz.Close()

	r, closer, err := Decompressor(&buf, FormatGzip)
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	got, _ := io.ReadAll(r)
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecompressorZstd(t *testing.T) {
	want := "zstd test payload"
	var buf bytes.Buffer
	w, _ := zstd.NewWriter(&buf)
	_, _ = w.Write([]byte(want))
	_ = w.Close()

	r, closer, err := Decompressor(&buf, FormatZstd)
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	got, _ := io.ReadAll(r)
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecompressorLZ4(t *testing.T) {
	want := "lz4 test payload"
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	_, _ = w.Write([]byte(want))
	_ = w.Close()

	r, closer, err := Decompressor(&buf, FormatLZ4)
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	got, _ := io.ReadAll(r)
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecompressorXZ(t *testing.T) {
	want := "xz test payload"
	var buf bytes.Buffer
	w, _ := xz.NewWriter(&buf)
	_, _ = w.Write([]byte(want))
	_ = w.Close()

	r, closer, err := Decompressor(&buf, FormatXZ)
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	got, _ := io.ReadAll(r)
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecompressorRaw(t *testing.T) {
	want := "raw content"
	r, closer, err := Decompressor(bytes.NewReader([]byte(want)), FormatRaw)
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	got, _ := io.ReadAll(r)
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDetectAndDecompressRoundTrip(t *testing.T) {
	// Compress with zstd, detect, decompress, verify.
	want := "roundtrip zstd content"
	var buf bytes.Buffer
	w, _ := zstd.NewWriter(&buf)
	_, _ = w.Write([]byte(want))
	_ = w.Close()

	f, reader, err := DetectFormat(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatZstd {
		t.Fatalf("detected %s, want zstd", f)
	}

	r, closer, err := Decompressor(reader, f)
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	got, _ := io.ReadAll(r)
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMatchBytes(t *testing.T) {
	tests := []struct {
		data   []byte
		prefix []byte
		want   bool
	}{
		{[]byte{0x1f, 0x8b, 0x00}, []byte{0x1f, 0x8b}, true},
		{[]byte{0x1f, 0x00}, []byte{0x1f, 0x8b}, false},
		{[]byte{0x28, 0xb5, 0x2f, 0xfd}, []byte{0x28, 0xb5, 0x2f, 0xfd}, true},
	}
	for _, tt := range tests {
		got := matchBytes(tt.data, tt.prefix)
		if got != tt.want {
			t.Errorf("matchBytes(%x, %x) = %v, want %v", tt.data, tt.prefix, got, tt.want)
		}
	}
}

func TestDetectFormatQCOW2(t *testing.T) {
	// qcow2 magic: QFI\xfb (0x51 0x46 0x49 0xfb)
	data := []byte{0x51, 0x46, 0x49, 0xfb, 0x00, 0x00}
	f, _, err := DetectFormat(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatQCOW2 {
		t.Errorf("got %s, want %s", f, FormatQCOW2)
	}
}

func TestDecompressorQCOW2_ReturnsError(t *testing.T) {
	_, _, err := Decompressor(bytes.NewReader(nil), FormatQCOW2)
	if err == nil {
		t.Fatal("expected error for qcow2 decompressor, got nil")
	}
}
