package linkcheck

import (
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/health"
)

func metricLinks() []Link {
	return []Link{
		{Ifname: "ens1f0np0", Carrier: true, Expected: &config.ExpectedLink{SpeedMbps: 25000},
			State: &LinkState{SpeedMbps: 10000, Flaps: 3, Errors: &Counters{RxErrors: 5, RxCRC: 5}, FEC: &FEC{Active: "rs", Uncorrectable: 2}},
			PCIe:  &PCIe{Speed: "8.0 GT/s PCIe", Width: 8, MaxSpeed: "16.0 GT/s PCIe", MaxWidth: 8}},
		{Ifname: "ens1f1np1", Carrier: true,
			State: &LinkState{SpeedMbps: 25000, Flaps: 2, Errors: &Counters{}, FEC: &FEC{Active: "off"}},
			PCIe:  &PCIe{Speed: "16.0 GT/s PCIe", Width: 4, MaxSpeed: "16.0 GT/s PCIe", MaxWidth: 8}},
		{Ifname: "eno1", State: &LinkState{SpeedMbps: 0, FEC: &FEC{Active: "off"}},
			PCIe: &PCIe{Speed: "5.0 GT/s PCIe", Width: 1, MaxSpeed: "5.0 GT/s PCIe", MaxWidth: 1}},
		// No carrier: the last known speed does not count.
		{Ifname: "eno2", State: &LinkState{SpeedMbps: 1000}},
	}
}

func TestLinkChecks(t *testing.T) {
	results := linkChecks(metricLinks(), &config.CheckThresholds{MinSpeedMbps: 25000})
	for _, tt := range []struct {
		name    string
		details []string
	}{
		{CheckLinkSpeed, []string{"ens1f0np0: 10000 Mb/s, want at least 25000", "ens1f0np0: 10000 Mb/s, want 25000"}},
		{CheckLinkFlaps, []string{"ens1f0np0: carrier lost 3 times"}},
		{CheckLinkErrors, []string{"ens1f0np0: 5 receive errors (CRC 5, frame 0), 0 transmit errors"}},
		{CheckFEC, []string{"ens1f0np0: 2 uncorrectable FEC blocks", "ens1f1np1: no FEC at 25000 Mb/s"}},
		{CheckPCIeLink, []string{"ens1f0np0: PCIe 8.0 GT/s PCIe x8, the device can 16.0 GT/s PCIe x8", "ens1f1np1: PCIe 16.0 GT/s PCIe x4, the device can 16.0 GT/s PCIe x8"}},
	} {
		r := findCheck(t, results, tt.name)
		if r.Status != health.StatusFail || r.Severity != health.SeverityWarning || r.Details != strings.Join(tt.details, "; ") {
			t.Errorf("%s = %+v\n want details %q", tt.name, r, strings.Join(tt.details, "; "))
		}
	}

	// Limits: three flaps and five errors are fine with these limits;
	// block makes FEC critical.
	links := metricLinks()
	results = linkChecks(links, &config.CheckThresholds{MaxFlaps: new(3), MaxErrors: 5, FEC: "block"})
	for _, name := range []string{CheckLinkFlaps, CheckLinkErrors, CheckLinkSpeed} {
		if r := findCheck(t, results, name); name != CheckLinkSpeed && r.Status != health.StatusPass {
			t.Errorf("%s with higher limits = %+v", name, r)
		}
	}
	if r := findCheck(t, results, CheckFEC); r.Severity != health.SeverityCritical {
		t.Errorf("fec with block = %+v", r)
	}
	// Without a minimum speed only the expected speed counts.
	if r := findCheck(t, results, CheckLinkSpeed); r.Details != "ens1f0np0: 10000 Mb/s, want 25000" {
		t.Errorf("link-speed without minimum = %+v", r)
	}
}

func TestGTS(t *testing.T) {
	for in, want := range map[string]float64{"16.0 GT/s PCIe": 16, "2.5 GT/s": 2.5, "": 0, "Unknown": 0} {
		if got := gts(in); got != want {
			t.Errorf("gts(%q) = %v, want %v", in, got, want)
		}
	}
}
