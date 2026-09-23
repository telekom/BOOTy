package serialconsole

import (
	"path/filepath"
	"testing"
)

func TestReadDeviceResourceUsesRangeStart(t *testing.T) {
	host := newFakeHost(t)
	dir := filepath.Join(host.sysRoot(), "class", "tty", "ttyS0")
	host.write(filepath.Join("sys", "class", "tty", "ttyS0", "device", "resource"),
		"000003f8-000003ff 000003f8-000003ff 00000100\n")

	ioPort, memBase := host.resolver().readDeviceResource(dir)
	if ioPort != 0x3f8 || memBase != 0 {
		t.Fatalf("readDeviceResource() = %#x, %#x; want %#x, 0", ioPort, memBase, uint64(0x3f8))
	}
}
