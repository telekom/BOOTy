package image

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VerifyGPGSignature downloads a detached GPG signature from sigURL and
// verifies it against the image at imageURL using the public key at
// pubKeyPath. It uses gpgv (preferred) or gpg --verify as fallback.
// The image body is streamed directly into GPG's stdin to avoid storing
// multi-GB images in memory.
func VerifyGPGSignature(ctx context.Context, imageURL, sigURL, pubKeyPath string) error {
	slog.Info("Verifying image GPG signature",
		"image", filepath.Base(imageURL),
		"signature", filepath.Base(sigURL),
		"pubkey", pubKeyPath,
	)

	if _, err := os.Stat(pubKeyPath); err != nil {
		return fmt.Errorf("GPG public key not found at %s: %w", pubKeyPath, err)
	}

	sigFile, err := downloadToTemp(ctx, sigURL, "booty-sig-*.sig")
	if err != nil {
		return fmt.Errorf("downloading signature: %w", err)
	}
	defer func() { _ = os.Remove(sigFile) }()

	return verifyWithStream(ctx, imageURL, pubKeyPath, sigFile)
}

// verifyWithStream opens an HTTP stream for the image and pipes it into
// gpgv/gpg --verify via stdin, avoiding a full download to disk/tmpfs.
func verifyWithStream(ctx context.Context, imageURL, keyring, sigFile string) error {
	if strings.HasPrefix(imageURL, "oci://") {
		return fmt.Errorf("GPG signature verification is not supported for OCI images (%s)", imageURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating image request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return fmt.Errorf("streaming image for verification: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image download for verification: status %d", resp.StatusCode)
	}

	return runGPGVerifyStream(ctx, keyring, sigFile, resp.Body)
}

// downloadToTemp downloads a URL to a temporary file and returns the path.
func downloadToTemp(ctx context.Context, rawURL, pattern string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", filepath.Base(rawURL), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: status %d", filepath.Base(rawURL), resp.StatusCode)
	}

	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(f.Name()) //nolint:gosec // self-created temp file, no traversal risk
		return "", fmt.Errorf("writing temp file: %w", err)
	}
	return f.Name(), nil
}

// runGPGVerifyStream executes gpgv or gpg --verify with the image data piped
// via stdin (using "-" as the data file argument) to avoid writing to disk.
func runGPGVerifyStream(ctx context.Context, keyring, sigFile string, data io.Reader) error {
	// Prefer gpgv (lightweight, no keyring management).
	if binPath, err := exec.LookPath("gpgv"); err == nil {
		cmd := exec.CommandContext(ctx, binPath, "--keyring", keyring, sigFile, "-")
		cmd.Stdin = data
		out, err := cmd.CombinedOutput()
		if err != nil {
			output := strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " | ")
			if len(output) > 256 {
				output = output[:256] + "..."
			}
			return fmt.Errorf("gpgv verification failed: %w (output: %s)", err, output)
		}
		slog.Info("GPG signature verified successfully (gpgv)")
		return nil
	}

	// Fall back to gpg --verify.
	if binPath, err := exec.LookPath("gpg"); err == nil {
		cmd := exec.CommandContext(ctx, binPath, "--no-default-keyring",
			"--keyring", keyring, "--verify", sigFile, "-")
		cmd.Stdin = data
		out, err := cmd.CombinedOutput()
		if err != nil {
			output := strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " | ")
			if len(output) > 256 {
				output = output[:256] + "..."
			}
			return fmt.Errorf("gpg signature verification failed: %w (output: %s)", err, output)
		}
		slog.Info("GPG signature verified successfully (gpg)")
		return nil
	}

	return fmt.Errorf("gpg tools not available: neither gpgv nor gpg found in PATH")
}
