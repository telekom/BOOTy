package linkcheck

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/health"
)

// Link check names.
const (
	CheckLinkSpeed  = "link-speed"
	CheckLinkFlaps  = "link-flaps"
	CheckLinkErrors = "link-errors"
	CheckFEC        = "fec"
	CheckPCIeLink   = "pcie-link"
)

// defaultMaxFlaps is the carrier losses since the driver loaded that are
// fine: the switch port may bounce once while the host boots.
const defaultMaxFlaps = 2

// linkChecks runs the link speed, flap, error, FEC and PCIe checks.
func linkChecks(links []Link, t *config.CheckThresholds) []health.CheckResult {
	maxFlaps := defaultMaxFlaps
	if t.MaxFlaps != nil {
		maxFlaps = *t.MaxFlaps
	}
	var speed, flaps, errs, fec, pci []string
	for i := range links {
		l := &links[i]
		if s := l.State; s != nil {
			speed = append(speed, speedProblems(l, t.MinSpeedMbps)...)
			if s.Flaps > maxFlaps {
				flaps = append(flaps, fmt.Sprintf("%s: carrier lost %d times", l.Ifname, s.Flaps))
			}
			if s.Errors != nil && s.Errors.RxErrors+s.Errors.TxErrors > t.MaxErrors {
				errs = append(errs, fmt.Sprintf("%s: %d receive errors (CRC %d, frame %d), %d transmit errors",
					l.Ifname, s.Errors.RxErrors, s.Errors.RxCRC, s.Errors.RxFrame, s.Errors.TxErrors))
			}
			fec = append(fec, fecProblems(l)...)
		}
		if p := l.PCIe; p != nil && (gts(p.Speed) < gts(p.MaxSpeed) || p.Width < p.MaxWidth) {
			pci = append(pci, fmt.Sprintf("%s: PCIe %s x%d, the device can %s x%d", l.Ifname, p.Speed, p.Width, p.MaxSpeed, p.MaxWidth))
		}
	}
	warn := config.CheckActionWarn
	return []health.CheckResult{
		result(CheckLinkSpeed, action(t.LinkSpeed, warn), speed),
		result(CheckLinkFlaps, action(t.LinkFlaps, warn), flaps),
		result(CheckLinkErrors, action(t.LinkErrors, warn), errs),
		result(CheckFEC, action(t.FEC, warn), fec),
		result(CheckPCIeLink, action(t.PCIe, warn), pci),
	}
}

func speedProblems(l *Link, minMbps int) []string {
	s := l.State.SpeedMbps
	if !l.Carrier || s == 0 {
		return nil
	}
	var out []string
	if minMbps > 0 && s < minMbps {
		out = append(out, fmt.Sprintf("%s: %d Mb/s, want at least %d", l.Ifname, s, minMbps))
	}
	if l.Expected != nil && l.Expected.SpeedMbps > 0 && s != l.Expected.SpeedMbps {
		out = append(out, fmt.Sprintf("%s: %d Mb/s, want %d", l.Ifname, s, l.Expected.SpeedMbps))
	}
	return out
}

// fecProblems: uncorrectable blocks, or no FEC on a 25G or faster link,
// where the standards require it.
func fecProblems(l *Link) []string {
	f := l.State.FEC
	if f == nil {
		return nil
	}
	var out []string
	if f.Uncorrectable > 0 {
		out = append(out, fmt.Sprintf("%s: %d uncorrectable FEC blocks", l.Ifname, f.Uncorrectable))
	}
	if l.Carrier && l.State.SpeedMbps >= 25000 && f.Active == "off" {
		out = append(out, fmt.Sprintf("%s: no FEC at %d Mb/s", l.Ifname, l.State.SpeedMbps))
	}
	return out
}

// gts parses a PCIe link speed such as "16.0 GT/s PCIe"; 0 when unknown.
func gts(s string) float64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}
