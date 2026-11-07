//go:build linux

package provision

import (
	"log/slog"
	"time"

	"github.com/telekom/BOOTy/pkg/health"
)

// The meminfo file and the sample interval of startMemoryPeak; tests
// replace them.
var (
	memoryPeakInfo     = "/proc/meminfo"
	memoryPeakInterval = time.Second
)

// startMemoryPeak samples MemAvailable while the image is written and
// returns a function that stops the sampling and logs the high-water mark of
// used RAM. An image staged in tmpfs counts as used RAM, so the log line
// shows what staging costs and what IMAGE_STREAM saves.
func startMemoryPeak(log *slog.Logger) func() {
	total, available := health.MemInfoMiB(memoryPeakInfo)
	if total == 0 {
		return func() {}
	}
	done := make(chan struct{})
	lowest := make(chan int64)
	go func() {
		low := available
		sample := func() {
			if _, a := health.MemInfoMiB(memoryPeakInfo); a > 0 && a < low {
				low = a
			}
		}
		ticker := time.NewTicker(memoryPeakInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sample()
			case <-done:
				sample()
				lowest <- low
				return
			}
		}
	}()
	return func() {
		close(done)
		low := <-lowest
		log.Info("image step memory", "peak_used_mib", total-low, "min_available_mib", low, "total_mib", total)
	}
}
