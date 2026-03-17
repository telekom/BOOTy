package telemetry

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCounter(t *testing.T) {
	c := &Counter{}
	if c.Value() != 0 {
		t.Errorf("initial = %d", c.Value())
	}
	c.Inc()
	c.Inc()
	c.Add(3)
	if c.Value() != 5 {
		t.Errorf("after ops = %d, want 5", c.Value())
	}
}

func TestGauge(t *testing.T) {
	g := &Gauge{}
	g.Set(42)
	if g.Value() != 42 {
		t.Errorf("gauge = %d, want 42", g.Value())
	}
	g.Set(0)
	if g.Value() != 0 {
		t.Errorf("gauge = %d, want 0", g.Value())
	}
}

func TestHistogram(t *testing.T) {
	h := &Histogram{}
	h.Observe(1.0)
	h.Observe(2.0)
	h.Observe(3.0)
	if h.Count() != 3 {
		t.Errorf("count = %d, want 3", h.Count())
	}
	if h.Sum() != 6.0 {
		t.Errorf("sum = %f, want 6.0", h.Sum())
	}
}

func TestHistogram_Empty(t *testing.T) {
	h := &Histogram{}
	if h.Count() != 0 {
		t.Errorf("count = %d", h.Count())
	}
	if h.Sum() != 0.0 {
		t.Errorf("sum = %f", h.Sum())
	}
}

func TestMetrics_Snapshot(t *testing.T) {
	m := NewMetrics()
	m.StepRetries.Add(3)
	m.StepErrors.Inc()
	m.ImageBytes.Add(1024)
	m.DiskCount.Set(4)
	m.NICCount.Set(2)
	m.CPUCount.Set(8)
	m.MemoryTotalBytes.Set(1073741824)
	m.StepDuration.Observe(1.5)
	m.StepDuration.Observe(2.5)

	snap := m.Snapshot()
	if snap.StepRetries != 3 {
		t.Errorf("retries = %d", snap.StepRetries)
	}
	if snap.StepErrors != 1 {
		t.Errorf("errors = %d", snap.StepErrors)
	}
	if snap.ImageBytes != 1024 {
		t.Errorf("imageBytes = %d", snap.ImageBytes)
	}
	if snap.DiskCount != 4 {
		t.Errorf("diskCount = %d", snap.DiskCount)
	}
	if snap.StepDurationCount != 2 {
		t.Errorf("stepDurationCount = %d", snap.StepDurationCount)
	}
	if snap.StepDurationSumS != 4.0 {
		t.Errorf("stepDurationSumS = %f", snap.StepDurationSumS)
	}
}

func TestMetricSnapshot_JSON(t *testing.T) {
	snap := &MetricSnapshot{StepRetries: 5, DiskCount: 2}
	data, err := snap.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded MetricSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.StepRetries != 5 {
		t.Errorf("retries = %d", decoded.StepRetries)
	}
}

func TestStepTracker_StartEnd(t *testing.T) {
	m := NewMetrics()
	tr := NewStepTracker(m, nil)
	tr.StartStep("disk-image")
	tr.EndStep("disk-image", nil)

	steps := tr.Steps()
	if len(steps) != 1 {
		t.Fatalf("steps = %d", len(steps))
	}
	if steps[0].Status != StatusDone {
		t.Errorf("status = %q", steps[0].Status)
	}
	if steps[0].Duration == 0 {
		t.Error("duration should be > 0")
	}
}

func TestStepTracker_Failed(t *testing.T) {
	m := NewMetrics()
	tr := NewStepTracker(m, nil)
	tr.StartStep("network")
	tr.EndStep("network", errors.New("timeout"))

	steps := tr.Steps()
	if steps[0].Status != StatusFailed {
		t.Errorf("status = %q", steps[0].Status)
	}
	if steps[0].Error != "timeout" {
		t.Errorf("error = %q", steps[0].Error)
	}
	if m.StepErrors.Value() != 1 {
		t.Errorf("errors = %d", m.StepErrors.Value())
	}
}

func TestStepTracker_Skip(t *testing.T) {
	tr := NewStepTracker(nil, nil)
	tr.SkipStep("luks")
	steps := tr.Steps()
	if len(steps) != 1 {
		t.Fatalf("steps = %d", len(steps))
	}
	if steps[0].Status != StatusSkipped {
		t.Errorf("status = %q", steps[0].Status)
	}
}

func TestStepTracker_Retry(t *testing.T) {
	m := NewMetrics()
	tr := NewStepTracker(m, nil)
	tr.StartStep("image")
	tr.RecordRetry("image")
	tr.RecordRetry("image")
	tr.EndStep("image", nil)

	steps := tr.Steps()
	if steps[0].Retries != 2 {
		t.Errorf("retries = %d, want 2", steps[0].Retries)
	}
	if m.StepRetries.Value() != 2 {
		t.Errorf("metric retries = %d", m.StepRetries.Value())
	}
}

func TestStepTracker_NilMetrics(t *testing.T) {
	tr := NewStepTracker(nil, nil)
	tr.StartStep("test")
	tr.RecordRetry("test")
	tr.EndStep("test", nil)
	steps := tr.Steps()
	if len(steps) != 1 {
		t.Errorf("steps = %d", len(steps))
	}
}

func TestStepTracker_TotalDuration(t *testing.T) {
	tr := NewStepTracker(nil, nil)
	tr.StartStep("a")
	tr.EndStep("a", nil)
	tr.StartStep("b")
	tr.EndStep("b", nil)
	if tr.TotalDuration() <= 0 {
		t.Error("total duration should be > 0")
	}
}
