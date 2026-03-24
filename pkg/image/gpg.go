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
)

// VerifyGPGSignature downloads a detached GPG signature from sigURL and
// verifies it against the image at imageURL using the public key at
// pubKeyPath. It uses gpgv (preferred) or gpg --verify as fallback.
// The image is downloaded to a temporary file for verification.
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

	imgFile, err := downloadToTemp(ctx, imageURL, "booty-img-*")
	if err != nil {
		return fmt.Errorf("downloading image for verification: %w", err)
	}
	defer func() { _ = os.Remove(imgFile) }()

	return runGPGVerify(ctx, pubKeyPath, sigFile, imgFile)
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

// runGPGVerify executes gpgv or gpg --verify to check the detached signature.
func runGPGVerify(ctx context.Context, keyring, sigFile, dataFile string) error {
	// Prefer gpgv (lightweight, no keyring management).
	if path, err := exec.LookPath("gpgv"); err == nil {
		cmd := exec.CommandContext(ctx, path, "--keyring", keyring, sigFile, dataFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("gpgv verification failed: %w\noutput: %s", err, string(out))
		}
		slog.Info("GPG signature verified successfully (gpgv)")
		return nil
	}

	// Fall back to gpg --verify.
	if path, err := exec.LookPath("gpg"); err == nil {
		cmd := exec.CommandContext(ctx, path, "--no-default-keyring",
			"--keyring", keyring, "--verify", sigFile, dataFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("gpg signature verification failed: %w\noutput: %s", err, string(out))
		}
		slog.Info("GPG signature verified successfully (gpg)")
		return nil
	}

	slog.Warn("Neither gpgv nor gpg available, skipping signature verification")
	return nil
}
