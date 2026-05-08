// Package crash collects startup crash artifacts and host metadata.
package crash

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// DefaultMaxBytes is the fallback archive payload cap used by the collector.
const DefaultMaxBytes int64 = 256 * 1024 * 1024

// ErrNoUploadURL is returned when crash artifact upload is requested without an endpoint.
var ErrNoUploadURL = errors.New("no crash artifact upload URL configured")

// Upload modes supported by CAPRF crash artifact upload instructions.
const (
	UploadModePresignedPUT  = "presigned-put"
	UploadModePresignedPOST = "presigned-post"
	UploadModeCAPRFProxy    = "caprf-proxy"

	AuthModeNone   = "none"
	AuthModeBearer = "bearer"
)

// MachineMetadata holds config-derived fields used for correlation.
type MachineMetadata struct {
	Hostname      string    `json:"hostname,omitempty"`
	ProviderID    string    `json:"providerId,omitempty"`
	Mode          string    `json:"mode,omitempty"`
	Region        string    `json:"region,omitempty"`
	FailureDomain string    `json:"failureDomain,omitempty"`
	ImageMode     string    `json:"imageMode,omitempty"`
	NetworkMode   string    `json:"networkMode,omitempty"`
	BGPPeerMode   string    `json:"bgpPeerMode,omitempty"`
	DiskDevice    string    `json:"diskDevice,omitempty"`
	CollectedAt   time.Time `json:"collectedAt"`
}

// MetadataError records a non-fatal metadata collection failure.
type MetadataError struct {
	Component string `json:"component"`
	Error     string `json:"error"`
}

// HostMetadata contains rich host metadata for correlation.
type HostMetadata struct {
	Machine   MachineMetadata `json:"machine"`
	Inventory json.RawMessage `json:"inventory,omitempty"`
	Firmware  json.RawMessage `json:"firmware,omitempty"`
	Debug     json.RawMessage `json:"debug,omitempty"`
	BuildInfo json.RawMessage `json:"buildInfo,omitempty"`
	Errors    []MetadataError `json:"errors,omitempty"`
}

// ScanMetadata describes the disk and artifact scan context.
type ScanMetadata struct {
	TargetDisk     string   `json:"targetDisk,omitempty"`
	RootPartition  string   `json:"rootPartition,omitempty"`
	MountPoint     string   `json:"mountPoint,omitempty"`
	PstorePath     string   `json:"pstorePath,omitempty"`
	EvidenceFound  bool     `json:"evidenceFound"`
	ArtifactCount  int      `json:"artifactCount"`
	SkippedCount   int      `json:"skippedCount"`
	TotalBytes     int64    `json:"totalBytes"`
	ArchiveBytes   int64    `json:"archiveBytes,omitempty"`
	SkipReasons    []string `json:"skipReasons,omitempty"`
	Unsupported    string   `json:"unsupported,omitempty"`
	CollectorError string   `json:"collectorError,omitempty"`
}

// Artifact describes one file included in the crash archive.
type Artifact struct {
	SourcePath  string   `json:"sourcePath"`
	ArchivePath string   `json:"archivePath"`
	Kind        string   `json:"kind"`
	SizeBytes   int64    `json:"sizeBytes"`
	Evidence    []string `json:"evidence,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
}

// SkippedArtifact describes an allowlisted file that was not archived.
type SkippedArtifact struct {
	SourcePath string `json:"sourcePath"`
	Reason     string `json:"reason"`
	Error      string `json:"error,omitempty"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
}

// Manifest is written into every crash archive as manifest.json.
type Manifest struct {
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"createdAt"`
	Scan      ScanMetadata      `json:"scan"`
	Metadata  HostMetadata      `json:"metadata"`
	Artifacts []Artifact        `json:"artifacts"`
	Skipped   []SkippedArtifact `json:"skipped,omitempty"`
}

// PrepareRequest is sent to CAPRF before uploading the archive.
type PrepareRequest struct {
	Manifest      Manifest `json:"manifest"`
	ArchiveBytes  int64    `json:"archiveBytes"`
	ArtifactCount int      `json:"artifactCount"`
	TotalBytes    int64    `json:"totalBytes"`
}

// PrepareResponse describes where and how BOOTy should upload the archive.
type PrepareResponse struct {
	ArtifactID string            `json:"artifactId,omitempty"`
	UploadURL  string            `json:"uploadUrl"`
	Method     string            `json:"method,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	FormFields map[string]string `json:"formFields,omitempty"`
	AuthMode   string            `json:"authMode,omitempty"`
	UploadMode string            `json:"uploadMode,omitempty"`
	MaxBytes   int64             `json:"maxBytes,omitempty"`
	ExpiresAt  time.Time         `json:"expiresAt,omitempty"`
	ExpiryUnix int64             `json:"expiryUnix,omitempty"`
}

// CollectResult is returned by the collector.
type CollectResult struct {
	Manifest      Manifest
	ArchivePath   string
	EvidenceFound bool
}

// InspectResult summarizes a startup inspection attempt.
type InspectResult struct {
	Ran           bool
	Uploaded      bool
	EvidenceFound bool
	ArchivePath   string
	SkipReason    string
	Manifest      *Manifest
	UploadError   error
}

// Uploader is implemented by CAPRF clients that can upload crash artifacts.
type Uploader interface {
	ReportCrashArtifacts(ctx context.Context, req *PrepareRequest, archivePath string) error
}
