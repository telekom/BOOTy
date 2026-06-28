package disk

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PartitionNumber extracts the partition number from a device node path.
// For example: /dev/sda1 -> 1, /dev/nvme0n1p2 -> 2.
func PartitionNumber(node, disk string) int {
	n, err := PartitionNumberChecked(node, disk)
	if err != nil {
		return 0
	}
	return n
}

// PartitionNumberChecked extracts the partition number without assuming node
// and disk share the same path prefix. This keeps stable disk selectors such as
// /dev/disk/by-id/... usable when the partition scanner reports /dev/sda2.
func PartitionNumberChecked(node, disk string) (int, error) {
	if node == "" {
		return 0, fmt.Errorf("partition node is empty")
	}
	if disk == "" {
		return 0, fmt.Errorf("disk path is empty")
	}
	suffix := partitionSuffix(node, disk)
	suffix = strings.TrimPrefix(suffix, "-part")
	if suffix != "" && suffix[0] == 'p' {
		suffix = suffix[1:]
	}
	n := 0
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("could not determine partition number from %q for disk %q", node, disk)
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return 0, fmt.Errorf("could not determine partition number from %q for disk %q", node, disk)
	}
	return n, nil
}

func partitionSuffix(node, disk string) string {
	if strings.HasPrefix(node, disk) {
		return node[len(disk):]
	}
	return trailingDigits(filepath.Base(node))
}

func trailingDigits(s string) string {
	end := len(s)
	start := end
	for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
		start--
	}
	return s[start:end]
}
