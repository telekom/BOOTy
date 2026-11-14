package caprf

import (
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

// IMAGE_FOLLOW_REDIRECTS defaults to true; CAPRF_LEGACY_COMPAT presets false
// (legacy curl without --location) and an explicit value wins.
func TestParseVarsImageFollowRedirects(t *testing.T) {
	for _, tt := range []struct {
		vars string
		want bool
	}{
		{vars: "HOSTNAME=x\n", want: true},
		{vars: "IMAGE_FOLLOW_REDIRECTS=false\n", want: false},
		{vars: "CAPRF_LEGACY_COMPAT=true\n", want: false},
		{vars: "CAPRF_LEGACY_COMPAT=true\nIMAGE_FOLLOW_REDIRECTS=true\n", want: true},
	} {
		cfg, err := ParseVars(strings.NewReader(tt.vars))
		if err != nil {
			t.Fatalf("ParseVars(%q): %v", tt.vars, err)
		}
		if got := config.BoolOr(cfg.Provision.Image.FollowRedirects, true); got != tt.want {
			t.Errorf("ParseVars(%q) follow redirects = %v, want %v", tt.vars, got, tt.want)
		}
	}
}
