// Package health provides a pre-provisioning hardware health check framework.
package health

import (
	"context"
	"strings"
)

// Severity represents the criticality of a health check.
type Severity string

// Severity constants for health check results.
const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Status represents the outcome of a health check.
type Status string

// Status constants for health check outcomes.
const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// CheckResult holds the result of a single health check.
type CheckResult struct {
	Name     string   `json:"name"`
	Status   Status   `json:"status"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message,omitempty"`
	Details  string   `json:"details,omitempty"`
}

// Check is an individual health check that inspects a hardware subsystem.
type Check interface {
	Name() string
	Severity() Severity
	Run(ctx context.Context) CheckResult
}

// RunAll executes all checks, skipping those in the skip list.
// Returns results and whether any critical check failed.
func RunAll(ctx context.Context, checks []Check, skipList string) ([]CheckResult, bool) {
	skips := parseSkipList(skipList)
	var results []CheckResult
	criticalFailure := false

	for _, c := range checks {
		if _, skip := skips[c.Name()]; skip {
			results = append(results, CheckResult{
				Name:     c.Name(),
				Status:   StatusSkip,
				Severity: c.Severity(),
				Message:  "skipped by configuration",
			})
			continue
		}

		result := c.Run(ctx)
		results = append(results, result)

		if result.Status == StatusFail && result.Severity == SeverityCritical {
			criticalFailure = true
		}
	}

	return results, criticalFailure
}

func parseSkipList(s string) map[string]struct{} {
	m := make(map[string]struct{})
	for _, name := range strings.Split(s, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			m[name] = struct{}{}
		}
	}
	return m
}
