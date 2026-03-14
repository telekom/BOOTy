//go:build linux

package provision

import "errors"

// TransientError wraps an error that may succeed on retry.
type TransientError struct {
	Err error
}

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// PermanentError wraps an error that will not succeed on retry.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

func isTransient(err error) bool {
	var transient *TransientError
	return errors.As(err, &transient)
}

func isPermanent(err error) bool {
	var permanent *PermanentError
	return errors.As(err, &permanent)
}
