package disk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWipeFS(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{name: "4MiB file", size: 4 << 20},
		{name: "2MiB file", size: 2 << 20},
		{name: "exactly 1MiB", size: 1 << 20},
		{name: "small file under 1MiB", size: 512},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "testdev")
			data := make([]byte, tt.size)
			for i := range data {
				data[i] = 0xFF
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}

			if err := WipeFS(path); err != nil {
				t.Fatalf("WipeFS() unexpected error: %v", err)
			}

			wiped, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(wiped) != tt.size {
				t.Fatalf("file size changed from %d to %d", tt.size, len(wiped))
			}

			// First wipeSize bytes (or all bytes if smaller) should be zero
			checkLen := len(wiped)
			if checkLen > wipeSize {
				checkLen = wipeSize
			}
			for i := 0; i < checkLen; i++ {
				if wiped[i] != 0 {
					t.Fatalf("byte %d not zeroed: %02x", i, wiped[i])
				}
			}

			// Last wipeSize bytes should also be zero for files > wipeSize
			if tt.size > wipeSize {
				for i := len(wiped) - wipeSize; i < len(wiped); i++ {
					if wiped[i] != 0 {
						t.Fatalf("tail byte %d not zeroed: %02x", i, wiped[i])
					}
				}
			}

			if tt.size > 2*wipeSize {
				mid := wipeSize
				if wiped[mid] != 0xFF {
					t.Fatalf("middle byte %d unexpectedly modified: %02x", mid, wiped[mid])
				}
			}
		})
	}
}

func TestWipeFS_nonexistent(t *testing.T) {
	err := WipeFS("/nonexistent/device")
	if err == nil {
		t.Fatal("expected error for nonexistent device")
	}
}
