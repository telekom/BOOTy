// metrics package provides provisioning metrics collection.
package metrics

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// StepMetrics captures timing and status for a single provisioning step.
type StepMetrics struct {
	Name     string        `json:"name"`
	Duration time.Duration `json:"duration"`
	Status   string        `json:"status"`
	Error    string        `json:"error,omitempty"`
}

// ImageMetrics captures image download performance.
type ImageMetrics struct {
	URL           string        `json:"url"`
	SizeBytes     int64         `json:"sizeBytes"`
	Duration      time.Duration `json:"duration"`
	SpeedMBps     float64       `json:"speedMBps"`
	ChecksumMatch bool          `json:"checksumMatch"`
}

// DiskMetrics captures disk write performance.
type DiskMetrics struct {
	DevicePath   string        `json:"devicePath"`
	WrittenBytes int64         `json:"writtenBytes"`
	Duration     time.Duration `json:"duration"`
	SpeedMBps    float64       `json:"speedMBps"`
}

// Summary holds all metrics collected during a provisioning run.
type Summary struct {
	StartTime time.Time     `json:"startTime"`
	EndTime   time.Time     `json:"endTime"`
	TotalDur  time.Duration `json:"totalDuration"`
	Steps     []StepMetrics `json:"steps"`
	Image     *ImageMetrics `json:"image,omitempty"`
	Disk      *DiskMetrics  `json:"disk,omitempty"`
}

// Collector accumulates metrics during provisioning.
type Collector struct {
	mu        sync.Mutex
	startTime time.Time
	steps     []StepMetrics
	image     *ImageMetrics
	disk      *DiskMetrics
}

// NewCollector creates a Collector and records the start time.
func NewCollector() *Collector {
	return &Collector{startTime: time.Now()}
}

// RecordStep records timing and status for a provisioning step.
func (c *Collector) RecordStep(name string, duration time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := StepMetrics{Name: name, Duration: duration, Status: "ok"}
	if err != nil {
		m.Status = "error"
		m.Error = err.Error()
	}
	c.steps = append(c.steps, m)
}

// RecordImage records image download metrics.
func (c *Collector) RecordImage(url string, sizeBytes int64, duration time.Duration, checksumOK bool) {
	speedMBps := 0.0
	if duration > 0 && sizeBytes > 0 {
		speedMBps = float64(sizeBytes) / duration.Seconds() / (1024 * 1024)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.image = &ImageMetrics{
		URL: url, SizeBytes: sizeBytes, Duration: duration,
		SpeedMBps: speedMBps, ChecksumMatch: checksumOK,
	}
}

// RecordDisk records disk write metrics.
func (c *Collector) RecordDisk(devicePath string, writtenBytes int64, duration time.Duration) {
	speedMBps := 0.0
	if duration > 0 && writtenBytes > 0 {
		speedMBps = float64(writtenBytes) / duration.Seconds() / (1024 * 1024)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disk = &DiskMetrics{
		DevicePath: devicePath, WrittenBytes: writtenBytes,
		Duration: duration, SpeedMBps: speedMBps,
	}
}

// Summarize returns the collected metrics as a Summary.
func (c *Collector) Summarize() Summary {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	steps := make([]StepMetrics, len(c.steps))
	copy(steps, c.steps)
	return Summary{
		StartTime: c.startTime, EndTime: now, TotalDur: now.Sub(c.startTime),
		Steps: steps, Image: c.image, Disk: c.disk,
	}
}

// Marshal returns the metrics summary as JSON bytes.
func (c *Collector) Marshal() ([]byte, error) {
	s := c.Summarize()
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshaling metrics: %w", err)
	}
	return data, nil
}
