//go:build !linux

package disk

import "fmt"

// RereadPartitions is only supported on Linux.
func RereadPartitions(device string) error {
	return fmt.Errorf("reread partitions is only supported on linux: %s", device)
}
