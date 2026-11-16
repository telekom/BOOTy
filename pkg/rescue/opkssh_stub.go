//go:build !linux || no_opkssh

package rescue

// newCertVerifier is not available: a build with the no_opkssh tag leaves
// out the openpubkey dependencies.
func newCertVerifier(*Config) (certVerifier, error) { return nil, errOPKUnsupported }
