//go:build linux

package provision

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
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
	o.log.Info("Starting dry-run — no destructive changes will be made")

	checks := []struct {
		name string
		fn   func(ctx context.Context) DryRunResult
	}{
		{"config-validation", o.dryRunConfigValidation},
		{"image-reachability", o.dryRunImageReachability},
		{"disk-detection", o.dryRunDiskDetection},
		{"health-checks", o.dryRunHealthChecks},
	}

	results := make([]DryRunResult, 0, len(checks))
	var failed int
	for _, c := range checks {
		result := c.fn(ctx)
		results = append(results, result)
		icon := "✓"
		switch result.Status {
		case DryRunWarn:
			icon = "⚠"
		case DryRunFail:
			icon = "✗"
			failed++
		}
		o.log.Info("Dry-run check", "step", result.Step, "status", icon, "message", result.Message)
	}

	var summary strings.Builder
	for _, r := range results {
		fmt.Fprintf(&summary, "[%s] %s: %s\n", r.Status, r.Step, r.Message)
	}

	if failed > 0 {
		msg := fmt.Sprintf("dry-run completed with %d failure(s):\n%s", failed, summary.String())
		_ = o.provider.ReportStatus(ctx, config.StatusError, msg)
		return fmt.Errorf("dry-run: %d check(s) failed", failed)
	}

	msg := fmt.Sprintf("dry-run passed all checks:\n%s", summary.String())
	_ = o.provider.ReportStatus(ctx, config.StatusSuccess, msg)
	return nil
}

func (o *Orchestrator) dryRunConfigValidation(_ context.Context) DryRunResult {
	if len(o.cfg.ImageURLs) == 0 {
		return DryRunResult{Step: "config-validation", Status: DryRunFail, Message: "no image URLs configured"}
	}
	if o.cfg.Hostname == "" {
		return DryRunResult{Step: "config-validation", Status: DryRunWarn, Message: "hostname not set"}
	}
	return DryRunResult{Step: "config-validation", Status: DryRunPass, Message: "configuration valid"}
}

func (o *Orchestrator) dryRunImageReachability(ctx context.Context) DryRunResult {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	for _, imgURL := range o.cfg.ImageURLs {
		// Skip non-HTTP sources (OCI registries validated via separate path).
		if strings.HasPrefix(imgURL, "oci://") {
			o.log.Info("Skipping OCI image reachability check", "url", imgURL)
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, imgURL, http.NoBody)
		if err != nil {
			return DryRunResult{Step: "image-reachability", Status: DryRunFail,
				Message: fmt.Sprintf("invalid image URL %s: %v", imgURL, err)}
		}
		resp, err := httpClient.Do(req) //nolint:gosec // URL from trusted config
		if err != nil {
			return DryRunResult{Step: "image-reachability", Status: DryRunFail,
				Message: fmt.Sprintf("image unreachable %s: %v", imgURL, err)}
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return DryRunResult{Step: "image-reachability", Status: DryRunFail,
				Message: fmt.Sprintf("image server returned %d for %s", resp.StatusCode, imgURL)}
		}
		o.log.Info("Image reachable", "url", imgURL, "status", resp.StatusCode)
	}
	return DryRunResult{Step: "image-reachability", Status: DryRunPass, Message: "all images reachable"}
}

func (o *Orchestrator) dryRunDiskDetection(ctx context.Context) DryRunResult {
	if o.cfg.DiskDevice != "" {
		if _, err := os.Stat(o.cfg.DiskDevice); err != nil {
			return DryRunResult{Step: "disk-detection", Status: DryRunFail,
				Message: fmt.Sprintf("configured disk %s not found: %v", o.cfg.DiskDevice, err)}
		}
		return DryRunResult{Step: "disk-detection", Status: DryRunPass,
			Message: fmt.Sprintf("configured disk %s exists", o.cfg.DiskDevice)}
	}

	d, err := o.disk.DetectDisk(ctx, o.cfg.MinDiskSizeGB)
	if err != nil {
		return DryRunResult{Step: "disk-detection", Status: DryRunFail,
			Message: fmt.Sprintf("no suitable disk: %v", err)}
	}
	return DryRunResult{Step: "disk-detection", Status: DryRunPass,
		Message: fmt.Sprintf("detected disk %s", d)}
}

func (o *Orchestrator) dryRunHealthChecks(_ context.Context) DryRunResult {
	if !o.cfg.HealthChecksEnabled {
		return DryRunResult{Step: "health-checks", Status: DryRunWarn, Message: "health checks disabled"}
	}
	return DryRunResult{Step: "health-checks", Status: DryRunPass, Message: "health checks enabled and will run"}
}
