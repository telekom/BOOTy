package health

import (
	"os"
	"strconv"
	"strings"
)

// MemInfoMiB returns MemTotal and MemAvailable of a meminfo file in MiB,
// or zeros when it can not be read.
func MemInfoMiB(path string) (total, available int64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = kb / 1024
		case "MemAvailable:":
			available = kb / 1024
		}
	}
	return total, available
}
