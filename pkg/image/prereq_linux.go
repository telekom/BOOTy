//go:build linux

package image

import (
	"context"
	"fmt"
)

// ValidateStreamingPrerequisites verifies tools that the detected image format
// will need later while provisioning is still before destructive disk steps.
func ValidateStreamingPrerequisites(ctx context.Context, source string) (Format, error) {
	if sourceLooksQCOW2(source) {
		if _, err := requireQCOW2Converter(); err != nil {
			return FormatQCOW2, fmt.Errorf("qcow2 image %s requires qemu-img before disk wipe: %w", RedactURL(source), err)
		}
		return FormatQCOW2, nil
	}

	format, err := ProbeSourceFormat(ctx, source)
	if err != nil {
		return FormatRaw, fmt.Errorf("%w: %w", ErrFormatProbe, err)
	}
	if format == FormatQCOW2 {
		if _, err := requireQCOW2Converter(); err != nil {
			return format, fmt.Errorf("qcow2 image %s requires qemu-img before disk wipe: %w", RedactURL(source), err)
		}
	}
	return format, nil
}
