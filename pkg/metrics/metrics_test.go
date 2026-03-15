// metrics package tests.
package metrics

import (
	"fmt"
	"testing"
	"time"
)

func TestRecordStep(t *testing.T) {
	c := NewCollector()
	c.RecordStep("detect-disk", 100*time.Millisecond, nil)

	s := c.Summarize()
	if len(s.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(s.Steps))
	}
	if s.Steps[0].Name != "detect-disk" {
		t.Errorf("step name = %s, want detect-disk", s.Steps[0].Name)
	}
	if s.Steps[0].Status != "ok" {
		t.Errorf("step status = %s, want ok", s.Steps[0].Status)
	}
}

func TestRecordStepError(t *testing.T) {
	c := NewCollector()
	c.RecordStep("mount-root", 50*time.Millisecond, fmt.Errorf("mount failed"))

	s := c.Summarize()
	if s.Steps[0].Status != "error" {
		t.Errorf("step status = %s, want error", s.Steps[0].Status)
	}
	if s.Steps[0].Error != "mount failed" {
		t.Errorf("step error = %s, want mount failed", s.Steps[0].Error)
	}
}

func TestRecordImage(t *testing.T) {
	c := NewCollector()
	c.RecordImage("http://example.com/image.gz", 1024*1024*100, 10*time.Second, true)

	s := c.Summarize()
	if s.Image == nil {
		t.Fatal("image metrics nil")
	}
	if s.Image.SizeBytes != 1024*1024*100 {
		t.Errorf("sizeBytes = %d, want %d", s.Image.SizeBytes, 1024*1024*100)
	}
	if s.Image.SpeedMBps < 9 || s.Image.SpeedMBps > 11 {
		t.Errorf("speed = %f, want ~10", s.Image.SpeedMBps)
	}
}

func TestRecordDisk(t *testing.T) {
	c := NewCollector()
	c.RecordDisk("/dev/sda", 1024*1024*500, 5*time.Second)

	s := c.Summarize()
	if s.Disk == nil {
		t.Fatal("disk metrics nil")
	}
	if s.Disk.SpeedMBps < 99 || s.Disk.SpeedMBps > 101 {
		t.Errorf("speed = %f, want ~100", s.Disk.SpeedMBps)
	}
}

func TestMarshal(t *testing.T) {
	c := NewCollector()
	c.RecordStep("test", time.Second, nil)

	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}
