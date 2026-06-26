//go:build !linux && linux_e2e

// Package linux intentionally has no tests on non-Linux hosts; this file keeps
// `go test -tags linux_e2e ./test/e2e/linux/...` discoverable without compiling
// Linux-only e2e sources.
package linux
