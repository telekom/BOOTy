package image

import (
	"fmt"
	"strings"

	"github.com/telekom/t-caas-go-library/pkg/redact"
)

// RedactURL strips credentials, query parameters, and fragments from source
// URLs before they are written to logs or returned in errors.
func RedactURL(rawURL string) string {
	return redact.URL(rawURL)
}

// RedactOCIRef strips credentials from an OCI reference that has already had
// its oci:// scheme removed.
func RedactOCIRef(ref string) string {
	return strings.TrimPrefix(RedactURL("oci://"+ref), "oci://")
}

// RedactSourceError removes the raw source URL from an error message while
// preserving the redacted source context.
func RedactSourceError(err error, rawSource string) string {
	if err == nil {
		return ""
	}
	if rawSource == "" {
		return err.Error()
	}
	return fmt.Sprintf("%s: %s", RedactURL(rawSource), redact.Wrap(err, rawSource))
}

type redactedSourceError struct {
	rawSource string
	err       error
}

func (e *redactedSourceError) Error() string {
	return RedactSourceError(e.err, e.rawSource)
}

func (e *redactedSourceError) Unwrap() error {
	return e.err
}

func redactOCIRefError(err error, ref string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	redactedRef := RedactOCIRef(ref)
	msg = strings.ReplaceAll(msg, ref, redactedRef)
	msg = strings.ReplaceAll(msg, "oci://"+ref, "oci://"+redactedRef)
	return msg
}

type redactedOCIRefError struct {
	ref string
	err error
}

func (e *redactedOCIRefError) Error() string {
	return redactOCIRefError(e.err, e.ref)
}

func (e *redactedOCIRefError) Unwrap() error {
	return e.err
}

func wrapRedactedOCIRefError(err error, ref string) error {
	if err == nil {
		return nil
	}
	return &redactedOCIRefError{ref: ref, err: err}
}
