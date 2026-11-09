package config

import "testing"

func TestReportTargetPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", DefaultReportPath},
		{"  ", DefaultReportPath},
		{"none", ""},
		{"NONE", ""},
		{"/var/lib/booty/report.json", "/var/lib/booty/report.json"},
	} {
		p := ProvisionConfig{ReportPath: tc.in}
		if got := p.ReportTargetPath(); got != tc.want {
			t.Errorf("ReportTargetPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateReportPath(t *testing.T) {
	for _, ok := range []string{"", "none", "/etc/booty/provision.json"} {
		if err := validateReportPath(ok); err != nil {
			t.Errorf("validateReportPath(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"relative.json", "/", "/etc/../x.json", "/etc//x.json"} {
		if err := validateReportPath(bad); err == nil {
			t.Errorf("validateReportPath(%q) accepted", bad)
		}
	}
}
