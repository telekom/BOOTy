package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WaitForHTTP polls target with HTTP HEAD until reachable.
func WaitForHTTP(ctx context.Context, target string, timeout time.Duration) error {
	if target == "" {
		return fmt.Errorf("empty connectivity target URL")
	}

	if timeout <= 0 {
		return fmt.Errorf("network connectivity timeout must be positive, got %s", timeout)
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{Timeout: 10 * time.Second}
	attempt := 0
	retryTicker := time.NewTicker(1 * time.Second)
	defer retryTicker.Stop()
	logTarget := RedactHTTPURLForLog(target)

	for {
		if err := waitCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("network connectivity timeout after %s (%d attempts): %w", timeout, attempt, err)
			}
			return fmt.Errorf("context canceled: %w", err)
		}

		attempt++
		req, err := http.NewRequestWithContext(waitCtx, http.MethodHead, target, http.NoBody)
		if err != nil {
			return fmt.Errorf("create request for %s: %w", logTarget, &redactedHTTPError{rawURL: target, err: err})
		}

		resp, err := client.Do(req) //nolint:gosec // target is from trusted config
		if err == nil {
			_ = resp.Body.Close()
			// Any HTTP response proves the network path works. The server
			// may return 401 (auth required) or other non-2xx codes, but
			// that still means connectivity is established.
			slog.Info("network connectivity established", "target", logTarget, "status", resp.StatusCode, "attempt", attempt)
			return nil
		}

		slog.Debug("connectivity check failed", "target", logTarget, "attempt", attempt, "error", redactHTTPErrorForLog(err, target))

		select {
		case <-waitCtx.Done():
			err := waitCtx.Err()
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("network connectivity timeout after %s (%d attempts): %w", timeout, attempt, err)
			}
			return fmt.Errorf("context canceled: %w", err)
		case <-retryTicker.C:
		}
	}
}

// RedactHTTPURLForLog strips credentials, query parameters, and fragments from
// HTTP connectivity target URLs before they are written to logs.
func RedactHTTPURLForLog(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "[redacted invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func redactHTTPErrorForLog(err error, rawURL string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	redacted := RedactHTTPURLForLog(rawURL)
	for _, candidate := range httpURLRedactionCandidates(rawURL) {
		msg = strings.ReplaceAll(msg, candidate, redacted)
	}
	return msg
}

type redactedHTTPError struct {
	rawURL string
	err    error
}

func (e *redactedHTTPError) Error() string {
	return redactHTTPErrorForLog(e.err, e.rawURL)
}

func (e *redactedHTTPError) Unwrap() error {
	return e.err
}

func httpURLRedactionCandidates(rawURL string) []string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return invalidHTTPURLRedactionCandidates(rawURL)
	}

	var candidates []string
	add := func(value string) {
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}

	add(rawURL)
	add(u.String())
	add(u.Redacted())

	withoutFragment := *u
	withoutFragment.Fragment = ""
	add(withoutFragment.String())
	add(withoutFragment.Redacted())

	addHTTPCredentialRedactionCandidates(add, u, &withoutFragment)

	return candidates
}

func invalidHTTPURLRedactionCandidates(rawURL string) []string {
	candidates := []string{rawURL}
	if withoutFragment, _, ok := strings.Cut(rawURL, "#"); ok && withoutFragment != "" {
		candidates = append(candidates, withoutFragment)
	}
	return candidates
}

func addHTTPCredentialRedactionCandidates(add func(string), u, withoutFragment *url.URL) {
	if u.User == nil {
		return
	}
	password, ok := u.User.Password()
	if !ok {
		return
	}

	username := u.User.Username()
	userInfo := u.User.String()
	if userInfo != "" {
		add(strings.Replace(u.String(), userInfo+"@", username+":***@", 1))
		add(strings.Replace(withoutFragment.String(), userInfo+"@", username+":***@", 1))
	}
	if password != "" {
		add(strings.Replace(u.String(), ":"+password+"@", ":***@", 1))
		add(strings.Replace(withoutFragment.String(), ":"+password+"@", ":***@", 1))
	}
	for _, placeholder := range []string{"xxxxx", "***"} {
		redactedPassword := *u
		redactedPassword.User = url.UserPassword(username, placeholder)
		add(redactedPassword.String())

		withoutFragment := redactedPassword
		withoutFragment.Fragment = ""
		add(withoutFragment.String())
	}
}
