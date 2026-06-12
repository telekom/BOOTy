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
		return "[redacted invalid URL]"
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
	redacted := RedactURL(rawSource)
	for _, candidate := range sourceRedactionCandidates(rawSource) {
		msg = strings.ReplaceAll(msg, candidate, redacted)
	}
	return msg
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

func sourceRedactionCandidates(rawSource string) []string {
	u, err := url.Parse(rawSource)
	if err != nil {
		return []string{rawSource}
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

	add(rawSource)
	add(u.String())
	add(u.Redacted())

	withoutFragment := *u
	withoutFragment.Fragment = ""
	add(withoutFragment.String())
	add(withoutFragment.Redacted())

	addSourceCredentialRedactionCandidates(add, u, &withoutFragment)

	return candidates
}

func addSourceCredentialRedactionCandidates(add func(string), u, withoutFragment *url.URL) {
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
