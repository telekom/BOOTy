package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// DefaultReportPath is where the provisioning report goes in the
	// installed OS, next to the A/B slot state (/etc/booty/ab-slot.env).
	DefaultReportPath = "/etc/booty/provision.json"
	// ReportPathNone writes no provisioning report into the installed OS.
	ReportPathNone = "none"
)

// ReportTargetPath returns the absolute path of the provisioning report in
// the installed OS, or "" when ReportPath is "none".
func (p *ProvisionConfig) ReportTargetPath() string {
	path := strings.TrimSpace(p.ReportPath)
	switch {
	case path == "":
		return DefaultReportPath
	case strings.EqualFold(path, ReportPathNone):
		return ""
	}
	return path
}

func validateReportPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || strings.EqualFold(path, ReportPathNone) {
		return nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return fmt.Errorf("provision.reportPath (PROVISION_REPORT_PATH) %q must be %q or a clean absolute file path", path, ReportPathNone)
	}
	return nil
}
