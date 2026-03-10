//go:build linux

package ux

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/zcalusic/sysinfo"
)

// SysInfo prints system hardware information to stdout.
func SysInfo() {
	var si sysinfo.SysInfo

	si.GetSysInfo()
	fmt.Println("")
	fmt.Println("------------ BOOTy System Information ------------")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)

	_, _ = fmt.Fprintf(w, "CPU:\t %s\n", si.CPU.Model)
	_, _ = fmt.Fprintf(w, "CPU speed:\t %dMHz\n", si.CPU.Speed)
	_, _ = fmt.Fprintf(w, "MEM size:\t %dMB\n", si.Memory.Size)
	for x := range si.Network {
		_, _ = fmt.Fprintf(w, "Network device:\t %s\n", si.Network[x].Name)
		_, _ = fmt.Fprintf(w, "Network driver:\t %s\n", si.Network[x].Driver)
		_, _ = fmt.Fprintf(w, "Network address:\t %s\n", si.Network[x].MACAddress)
	}
	for x := range si.Storage {
		_, _ = fmt.Fprintf(w, "Storage device:\t %s\n", si.Storage[x].Name)
		_, _ = fmt.Fprintf(w, "Storage driver:\t %s\n", si.Storage[x].Driver)
		_, _ = fmt.Fprintf(w, "Storage size:\t %dGB\n", si.Storage[x].Size)
	}
	_ = w.Flush()

	fmt.Println("--------------------------------------------------")
	fmt.Println("")

}
