package image

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dustin/go-humanize"
)

// WriteCounter counts the number of bytes written to it. It implements to the io.Writer interface
// and we can pass this into io.TeeReader() which will report progress on each write cycle.
type WriteCounter struct {
	Total atomic.Uint64
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Total.Add(uint64(n))
	return n, nil
}

// tickerProgress prints download progress to the console.
// Uses fmt.Printf intentionally for inline progress bar UX (not slog).
func tickerProgress(byteCounter uint64) {
	// Clear the line by using a character return to go back to the start and remove
	// the remaining characters by filling it with spaces
	fmt.Printf("\r%s", strings.Repeat(" ", 35))

	// Return again and print current status of download
	// We use the humanize package to print the bytes in a meaningful way (e.g. 10 MB)
	fmt.Printf("\rDownloading... %s complete", humanize.Bytes(byteCounter))
}

// startProgressTicker starts a background goroutine that logs download progress
// every 2 seconds. Returns a stop function that cleanly shuts down the goroutine.
func startProgressTicker(counter *WriteCounter) func() {
	ticker := time.NewTicker(2 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				tickerProgress(counter.Total.Load())
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
	}
}
