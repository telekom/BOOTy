// Package telemetry provides provisioning metrics, step timing, and event emission.
package telemetry

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// Counter is a monotonically increasing counter.
type Counter struct {
	value atomic.Int64
}

// Inc increments the counter by 1.
func (c *Counter) Inc() { c.value.Add(1) }

// Add adds delta to the counter. Negative deltas are ignored
// because counters are monotonically increasing.
func (c *Counter) Add(delta int64) {
	if delta > 0 {
		c.value.Add(delta)
	}
}

// Value returns the current counter value.
func (c *Counter) Value() int64 { return c.value.Load() }

// Gauge is a value that can go up and down.
type Gauge struct {
	value atomic.Int64
}

// Set sets the gauge to the given value.
func (g *Gauge) Set(v int64) { g.value.Store(v) }

// Value returns the current gauge value.
func (g *Gauge) Value() int64 { return g.value.Load() }

// Histogram records value distributions.
type Histogram struct {
	mu     sync.Mutex
	values []float64
}

// Observe records a value.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.values = append(h.values, v)
}

// Count returns the number of observations.
func (h *Histogram) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.values)
}

// Sum returns the sum of all observations.
func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var s float64
	for _, v := range h.values {
		s += v
	}
	return s
}

// CountAndSum atomically returns both count and sum in a single lock acquisition.
func (h *Histogram) CountAndSum() (count int, sum float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var s float64
	for _, v := range h.values {
		s += v
	}
	return len(h.values), s
}

// Metrics holds all provisioning metrics.
type Metrics struct {
	StepDuration      Histogram // seconds per step.
	StepRetries       Counter
	StepErrors        Counter
	ProvisionDuration Histogram // total provision time in seconds.
	ImageBytes        Counter   // total bytes streamed.
	DiskCount         Gauge
	NICCount          Gauge
	MemoryTotalBytes  Gauge
	CPUCount          Gauge
}

// NewMetrics creates a new Metrics instance.
func NewMetrics() *Metrics {
	return &Metrics{}
}

// Snapshot returns a JSON-serializable snapshot of all metrics.
func (m *Metrics) Snapshot() *MetricSnapshot {
	stepCount, stepSum := m.StepDuration.CountAndSum()
	provCount, provSum := m.ProvisionDuration.CountAndSum()
	return &MetricSnapshot{
		StepDurationCount:     stepCount,
		StepDurationSumS:      stepSum,
		StepRetries:           m.StepRetries.Value(),
		StepErrors:            m.StepErrors.Value(),
		ProvisionCount:        provCount,
		ProvisionDurationSumS: provSum,
		ImageBytes:            m.ImageBytes.Value(),
		DiskCount:             m.DiskCount.Value(),
		NICCount:              m.NICCount.Value(),
		MemoryTotalBytes:      m.MemoryTotalBytes.Value(),
		CPUCount:              m.CPUCount.Value(),
	}
}

// MetricSnapshot is a point-in-time view of metrics for JSON export.
type MetricSnapshot struct {
	StepDurationCount     int     `json:"stepDurationCount"`
	StepDurationSumS      float64 `json:"stepDurationSumS"`
	StepRetries           int64   `json:"stepRetries"`
	StepErrors            int64   `json:"stepErrors"`
	ProvisionCount        int     `json:"provisionCount"`
	ProvisionDurationSumS float64 `json:"provisionDurationSumS"`
	ImageBytes            int64   `json:"imageBytes"`
	DiskCount             int64   `json:"diskCount"`
	NICCount              int64   `json:"nicCount"`
	MemoryTotalBytes      int64   `json:"memoryTotalBytes"`
	CPUCount              int64   `json:"cpuCount"`
}

// JSON returns the snapshot as JSON bytes.
func (s *MetricSnapshot) JSON() ([]byte, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal metric snapshot: %w", err)
	}
	return data, nil
}
