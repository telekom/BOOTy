package image

import (
	"testing"
)

func TestWriteCounter(t *testing.T) {
	wc := &WriteCounter{}
	data := []byte("hello world")
	n, err := wc.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if wc.Total.Load() != uint64(len(data)) {
		t.Errorf("expected Total=%d, got %d", len(data), wc.Total.Load())
	}

	// Write more data
	n2, err := wc.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n2 != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n2)
	}
	if wc.Total.Load() != uint64(2*len(data)) {
		t.Errorf("expected Total=%d, got %d", 2*len(data), wc.Total.Load())
	}
}
