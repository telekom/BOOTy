//go:build linux

package provision

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
)

var (
	listInterfaces = net.Interfaces
	statPath       = os.Stat
)

// DryRunStatus represents the result status of a dry-run check.
type DryRunStatus string

// DryRunPass, DryRunWarn, and DryRunFail represent dry-run check outcomes.
const (
	DryRunPass DryRunStatus = "pass"
	DryRunWarn DryRunStatus = "warn"
	DryRunFail DryRunStatus = "fail"
)

// DryRunResult holds the result of a single dry-run check.
type DryRunResult struct {
	Step    string       `json:"step"`
	Status  DryRunStatus `json:"status"`
	Message string       `json:"message"`
}

// DryRun executes the provisioning pipeline in simulation mode.
func (o *Orchestrator) DryRun(ctx context.Context) error {
	o.log.Info("starting dry-run - no destructive changes will be made")

	checks := []struct {
		name string
		fn   func(ctx context.Context) DryRunResult
	}{
		{"config-validation", o.dryRunConfigValidation},
		{"image-reachability", o.dryRunImageReachability},
		{"image-checksum", o.dryRunImageChecksum},
		{"disk-detection", o.dryRunDiskDetection},
		{"network-link", o.dryRunNetworkLink},
		{"efi-boot", o.dryRunEFIBoot},
		{"health-checks", o.dryRunHealthChecks},
		{"inventory-probe", o.dryRunInventoryProbe},
	}

	results := make([]DryRunResult, 0, len(checks))
	var failed int
	for _, c := range checks {
		result := c.fn(ctx)
		result.Step = c.name // authoritative step name from the check table
		results = append(results, result)
		if result.Status == DryRunFail {
			failed++
		}
		o.log.Info("dry-run check", "step", result.Step, "status", string(result.Status), "message", result.Message)
	}

	var summary strings.Builder
	for _, r := range results {
		fmt.Fprintf(&summary, "[%s] %s: %s\n", r.Status, r.Step, r.Message)
	}

	if failed > 0 {
		msg := fmt.Sprintf("dry-run completed with %d failure(s):\n%s", failed, summary.String())
		if err := o.provider.ReportStatus(ctx, config.StatusError, msg); err != nil {
			o.log.Warn("failed to report dry-run status", "error", err)
		}
		return fmt.Errorf("dry-run: %d check(s) failed", failed)
	}

	msg := fmt.Sprintf("dry-run passed all checks:\n%s", summary.String())
	if err := o.provider.ReportStatus(ctx, config.StatusSuccess, msg); err != nil {
		o.log.Warn("failed to report dry-run status", "error", err)
	}
	return nil
}

func (o *Orchestrator) dryRunConfigValidation(_ context.Context) DryRunResult {
	if len(o.cfg.ImageURLs) == 0 {
		return DryRunResult{Status: DryRunFail, Message: "no image URLs configured"}
	}
	if o.cfg.Hostname == "" {
		return DryRunResult{Status: DryRunWarn, Message: "hostname not set"}
	}
	return DryRunResult{Status: DryRunPass, Message: "configuration valid"}
}

func (o *Orchestrator) dryRunImageReachability(ctx context.Context) DryRunResult {
	if len(o.cfg.ImageURLs) == 0 {
		return DryRunResult{Status: DryRunFail, Message: "no image URLs configured"}
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	validated := 0
	skippedOCI := 0
	for _, imgURL := range o.cfg.ImageURLs {
		redactedURL := redactImageURL(imgURL)
		parsedURL, err := url.Parse(imgURL)
		if err != nil || parsedURL.Scheme == "" {
			errMsg := redactURLError(err, imgURL)
			if errMsg == "" {
				errMsg = "missing URL scheme"
			}
			return DryRunResult{Status: DryRunFail,
				Message: fmt.Sprintf("invalid image URL %s: %s", redactedURL, errMsg)}
		}

		scheme := strings.ToLower(strings.TrimSpace(parsedURL.Scheme))
		// Skip OCI sources until registry reachability checks are implemented.
		if scheme == "oci" {
			skippedOCI++
			o.log.Info("skipping oci image reachability check", "url", redactedURL)
			continue
		}
		// Validate URL scheme is http/https before making outbound request.
		if scheme != "http" && scheme != "https" {
			return DryRunResult{Status: DryRunFail,
				Message: fmt.Sprintf("unsupported URL scheme: %s", redactedURL)}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, imgURL, http.NoBody)
		if err != nil {
			errMsg := redactURLError(err, imgURL)
			return DryRunResult{Status: DryRunFail,
				Message: fmt.Sprintf("invalid image URL %s: %s", redactedURL, errMsg)}
		}
		resp, err := httpClient.Do(req) //nolint:gosec // URL from trusted config
		if err != nil {
			errMsg := redactURLError(err, imgURL)
			return DryRunResult{Status: DryRunFail,
				Message: fmt.Sprintf("image unreachable %s: %s", redactedURL, errMsg)}
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return DryRunResult{Status: DryRunFail,
				Message: fmt.Sprintf("image server returned %d for %s", resp.StatusCode, redactedURL)}
		}
		validated++
		o.log.Info("image reachable", "url", redactedURL, "status", resp.StatusCode)
	}
	if validated == 0 && skippedOCI > 0 {
		return DryRunResult{
			Status:  DryRunWarn,
			Message: fmt.Sprintf("skipped %d OCI image URL(s); reachability not validated", skippedOCI),
		}
	}
	if skippedOCI > 0 {
		return DryRunResult{
			Status:  DryRunWarn,
			Message: fmt.Sprintf("validated %d HTTP image URL(s), skipped %d OCI image URL(s)", validated, skippedOCI),
		}
	}
	return DryRunResult{Status: DryRunPass, Message: "all HTTP image URLs reachable"}
}

func (o *Orchestrator) dryRunDiskDetection(ctx context.Context) DryRunResult {
	if o.cfg.DiskDevice != "" {
		info, err := statPath(o.cfg.DiskDevice)
		if err != nil {
			return DryRunResult{Status: DryRunFail,
				Message: fmt.Sprintf("configured disk %s not found: %v", o.cfg.DiskDevice, err)}
		}
		// Reject character devices (e.g. /dev/null). This is intentionally
		// stricter than the real provisioning path to catch misconfigurations
		// early during dry-run validation.
		if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
			return DryRunResult{Status: DryRunFail,
				Message: fmt.Sprintf("configured disk %s is not a block device", o.cfg.DiskDevice)}
		}
		return DryRunResult{Status: DryRunPass,
			Message: fmt.Sprintf("configured disk %s exists", o.cfg.DiskDevice)}
	}

	d, err := o.disk.DetectDisk(ctx, o.cfg.MinDiskSizeGB)
	if err != nil {
		return DryRunResult{Status: DryRunFail,
			Message: fmt.Sprintf("no suitable disk: %v", err)}
	}
	return DryRunResult{Status: DryRunPass,
		Message: fmt.Sprintf("detected disk %s", d)}
}

func (o *Orchestrator) dryRunHealthChecks(_ context.Context) DryRunResult {
	if !o.cfg.HealthChecksEnabled {
		return DryRunResult{Status: DryRunWarn, Message: "health checks disabled"}
	}
	return DryRunResult{Status: DryRunPass, Message: "health checks enabled and will run"}
}

func (o *Orchestrator) dryRunImageChecksum(_ context.Context) DryRunResult {
	if o.cfg.ImageChecksum == "" {
		return DryRunResult{Status: DryRunWarn,
			Message: "no image checksum configured - integrity cannot be verified"}
	}
	checkType := strings.ToLower(strings.TrimSpace(o.cfg.ImageChecksumType))
	if checkType == "" {
		checkType = "sha256"
	}
	switch checkType {
	case "sha256", "sha512":
		return DryRunResult{Status: DryRunPass,
			Message: fmt.Sprintf("checksum configured (%s)", checkType)}
	default:
		return DryRunResult{Status: DryRunFail,
			Message: fmt.Sprintf("unsupported checksum type: %s", checkType)}
	}
}

func (o *Orchestrator) dryRunNetworkLink(_ context.Context) DryRunResult {
	ifaces, err := listInterfaces()
	if err != nil {
		return DryRunResult{Status: DryRunFail,
			Message: fmt.Sprintf("cannot list interfaces: %v", err)}
	}

	var upIfaces []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Skip virtual interfaces (veth, docker, bridges).
		if isVirtualInterface(iface.Name) {
			continue
		}
		if iface.Flags&net.FlagUp != 0 {
			upIfaces = append(upIfaces, iface.Name)
		}
	}

	if len(upIfaces) == 0 {
		return DryRunResult{Status: DryRunFail,
			Message: "no physical non-loopback interfaces are up"}
	}
	return DryRunResult{Status: DryRunPass,
		Message: fmt.Sprintf("interfaces up: %s", strings.Join(upIfaces, ", "))}
}

// isVirtualInterface returns true for known virtual interface name prefixes.
func isVirtualInterface(name string) bool {
	virtualPrefixes := []string{"veth", "docker", "br-", "virbr", "cni", "flannel", "cali", "tunl", "vxlan"}
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (o *Orchestrator) dryRunEFIBoot(_ context.Context) DryRunResult {
	// Check EFI variables directory exists
	if _, err := statPath("/sys/firmware/efi"); err != nil {
		return DryRunResult{Status: DryRunWarn,
			Message: "system not booted in EFI mode"}
	}
	return DryRunResult{Status: DryRunPass,
		Message: "EFI firmware detected"}
}

func redactImageURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func redactURLError(err error, rawURL string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if rawURL == "" {
		return msg
	}
	return strings.ReplaceAll(msg, rawURL, redactImageURL(rawURL))
}

func (o *Orchestrator) dryRunInventoryProbe(_ context.Context) DryRunResult {
	if !o.cfg.InventoryEnabled {
		return DryRunResult{Status: DryRunWarn,
			Message: "hardware inventory disabled"}
	}
	// Check DMI data accessible
	if _, err := statPath("/sys/class/dmi/id/sys_vendor"); err != nil {
		return DryRunResult{Status: DryRunWarn,
			Message: "DMI data not accessible"}
	}
	return DryRunResult{Status: DryRunPass,
		Message: "hardware inventory enabled, DMI accessible"}
}
