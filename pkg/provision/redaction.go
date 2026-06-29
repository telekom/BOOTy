//go:build linux

package provision

import "regexp"

var (
	debugAuthorizationPattern = regexp.MustCompile(`(?i)(^|[\s,;&?"])(authorization)\s*:\s*([^"\s,;]+)`)
	debugBearerPattern        = regexp.MustCompile(`(?i)\bBearer\s+["']?[^"'\s,;]+["']?`)
	debugURLCredentialPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^@\s/]+@`)
	debugAssignmentPattern    = regexp.MustCompile(
		`(?i)(^|[\s,;&?])` +
			`([A-Za-z0-9_.-]*(?:password|passwd|token|secret|credential|authorization|auth|private)[A-Za-z0-9_.-]*|` +
			`(?:api[-_]?key|secret[-_]?key|private[-_]?key|access[-_]?key|key))` +
			`\s*([=:])\s*("[^"]*"|'[^']*'|[^\s,;&]+)`)
	debugWordValuePattern = regexp.MustCompile(`(?i)\b(password|passwd|token|secret|credential|authorization)\s+("[^"]*"|'[^']*'|[^\s,;]+)`)
)

func redactCommand(line string) string {
	redacted := redactCommandValues(line)
	redacted = debugAuthorizationPattern.ReplaceAllString(redacted, `${1}${2}: [REDACTED]`)
	redacted = debugBearerPattern.ReplaceAllString(redacted, `Bearer [REDACTED]`)
	redacted = debugURLCredentialPattern.ReplaceAllString(redacted, `${1}[REDACTED]@`)
	redacted = debugAssignmentPattern.ReplaceAllString(redacted, `${1}${2}${3}[REDACTED]`)
	redacted = debugWordValuePattern.ReplaceAllString(redacted, `${1} [REDACTED]`)
	return redacted
}

func redactDebugData(line string) string {
	return redactCommand(line)
}
