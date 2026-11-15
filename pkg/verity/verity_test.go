package verity

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFormatMatchesVeritysetup checks the hash tree with veritysetup, the
// reference implementation: one, two and three hash levels.
func TestFormatMatchesVeritysetup(t *testing.T) {
	if _, err := exec.LookPath("veritysetup"); err != nil {
		t.Skip("veritysetup not installed")
	}
	for _, blocks := range []int{2, 128, 129, 128*128 + 1} {
		dir := t.TempDir()
		data, hash := filepath.Join(dir, "data"), filepath.Join(dir, "hash")
		buf := make([]byte, blocks*blockSize)
		_, _ = rand.Read(buf)
		if err := os.WriteFile(data, buf, 0o600); err != nil {
			t.Fatal(err)
		}
		root, err := Format(data, hash)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.CommandContext(t.Context(), "veritysetup", "verify", data, hash, root).CombinedOutput(); err != nil {
			t.Fatalf("%d blocks: veritysetup verify: %v: %s", blocks, err, out)
		}
		buf[blockSize+7] ^= 1
		if err := os.WriteFile(data, buf, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := exec.CommandContext(t.Context(), "veritysetup", "verify", data, hash, root).Run(); err == nil {
			t.Fatalf("%d blocks: tampered data verified", blocks)
		}
	}
}

func TestFormatRejectsPartialBlocks(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(data, make([]byte, blockSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Format(data, data+".verity"); err == nil {
		t.Fatal("Format accepted a partial block")
	}
}
