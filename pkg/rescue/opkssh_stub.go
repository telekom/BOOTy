//go:build !linux || no_opkssh

package rescue

import "context"

// startOPKSSH is not available: the micro flavor builds with no_opkssh to
// keep the openpubkey dependencies out of its binary.
func startOPKSSH(context.Context, *Config) error { return errOPKUnsupported }
