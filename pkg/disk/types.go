package disk

// PartitionNumber extracts the partition number from a device node path.
// For example: /dev/sda1 -> 1, /dev/nvme0n1p2 -> 2.
func PartitionNumber(node, disk string) int {
	suffix := node[len(disk):]
	// Strip leading 'p' for NVMe-style names (nvme0n1p1).
	if suffix != "" && suffix[0] == 'p' {
		suffix = suffix[1:]
	}
	n := 0
	for _, c := range suffix {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
