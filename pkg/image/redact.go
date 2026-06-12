package image

import (
	"net/url"
	"strings"
)

// RedactURL strips credentials, query parameters, and fragments from source
// URLs before they are written to logs or returned in errors.
func RedactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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
	msg := err.Error()
	if rawSource == "" {
		return msg
	}
	return strings.ReplaceAll(msg, rawSource, RedactURL(rawSource))
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
