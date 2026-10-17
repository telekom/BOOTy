//go:build linux

package provision

import "github.com/telekom/BOOTy/pkg/logging"

func redactCommand(line string) string {
	return logging.RedactText(redactCommandValues(line))
}

func redactDebugData(line string) string {
	return redactCommand(line)
}
